// Package coretest holds test helpers over the coreref reference implementations:
// a memory-backed core.Deps, the World fixture builder (memory and, optionally,
// the same facts written to a storetest database), FakePasskeys, FixedKeys and a
// fixed Clock. Test-only (imports testing); production code never imports it.
//
// Owner: SCAFFOLD (contract steward during phases).
package coretest

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/coreref"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/clock"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/ids"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/refs"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Epoch is the fixed test clock start.
var Epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Clock returns a fake clock at Epoch.
func Clock() *clock.Fake { return clock.NewFake(Epoch) }

// MasterKeyHex is the fixed test master key.
const MasterKeyHex = "0f1e2d3c4b5a69788796a5b4c3d2e1f00f1e2d3c4b5a69788796a5b4c3d2e1f0"

// Config returns a PCC_ENV=test configuration with fixed keys.
func Config(t testing.TB, extra map[string]string) *config.Config {
	t.Helper()
	env := map[string]string{"PCC_ENV": "test", "PCC_MASTER_KEY": MasterKeyHex, "PCC_SERVER_KEY_FILE": "unused"}
	for k, v := range extra {
		env[k] = v
	}
	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("coretest: config: %v", err)
	}
	return cfg
}

// FixedKeys returns a deterministic ref codec and server signing key.
func FixedKeys(t testing.TB) (core.Refs, *sigs.Key) {
	t.Helper()
	k := sha256.Sum256([]byte("pcc-test-ref-key-a"))
	k2 := sha256.Sum256([]byte("pcc-test-ref-key-b"))
	codec, err := refs.New(map[byte][]byte{1: append(k[:], k2[:]...)}, 1)
	if err != nil {
		t.Fatal(err)
	}
	seed := sha256.Sum256([]byte("pcc-test-server-key"))
	key, err := sigs.KeyFromSeed(seed[:])
	if err != nil {
		t.Fatal(err)
	}
	key.From = Epoch
	return core.Refs{C: codec}, key
}

// Mem is a fully memory-backed core.Deps with handles to the reference types.
type Mem struct {
	D        *core.Deps
	Clock    *clock.Fake
	Users    *coreref.MemUsers
	Graph    *coreref.MemGraph
	Posts    *coreref.MemPosts
	Engage   *coreref.MemEngage
	Edges    *coreref.MemEdges
	Mutes    *coreref.MemMutes
	Notifier *coreref.RecordingNotifier
	Metrics  *coreref.MemMetrics
	Signer   *coreref.ServerSigner
	ModQueue *coreref.MemModQueue
	Requests *coreref.MemRequests
}

// NewMem builds a memory-backed core.Deps (no database) wired to coreref.
func NewMem(t testing.TB) *Mem {
	t.Helper()
	r, key := FixedKeys(t)
	m := &Mem{Clock: Clock(), Users: coreref.NewMemUsers(), Graph: coreref.NewMemGraph(), Engage: coreref.NewMemEngage(),
		Edges: coreref.NewMemEdges(), Mutes: &coreref.MemMutes{}, Metrics: &coreref.MemMetrics{},
		Signer: coreref.NewServerSigner(key), ModQueue: &coreref.MemModQueue{}, Requests: &coreref.MemRequests{}}
	d := &core.Deps{Clock: m.Clock, IDs: &ids.Seq{}, Refs: r, Cfg: Config(t, nil), Bus: &core.Bus{}}
	m.Posts = coreref.NewMemPosts(d)
	m.Notifier = &coreref.RecordingNotifier{D: d}
	vis := &coreref.MemVisibility{D: d}
	d.Users, d.Graph, d.Edges, d.Mutes, d.Posts = m.Users, m.Graph, m.Edges, m.Mutes, m.Posts
	d.Vis, d.Chains, d.Engage, d.Prefer = vis, &coreref.MemChains{D: d}, m.Engage, &coreref.MemPrefer{D: d}
	d.Views, d.Signer, d.Passkeys = &coreref.MemViews{D: d}, m.Signer, &FakePasskeys{}
	inv := &coreref.MemInvites{D: d, Users: m.Users}
	d.Invites, d.Probation, d.Notify, d.Limits, d.Metrics = inv, inv, m.Notifier, coreref.PassLimiter{}, m.Metrics
	d.Cosign, d.Handshakes, d.Media = &coreref.MemCosign{D: d}, coreref.MemHandshakes{}, coreref.MemMedia{}
	d.ModQueue, d.StepUp = m.ModQueue, &coreref.MemStepUp{D: d}
	d.DisplayNames = coreref.DisplayNameFunc(func(s string) (string, bool) { return s, true })
	d.RecordExporter, d.PostAdmin, d.Enrollments = m.Signer, &coreref.MemPostAdmin{D: d}, &coreref.MemEnrollments{}
	d.Mentions, d.Requests, d.Accounts = m.Posts, m.Requests, m.Users
	m.D = d
	return m
}

// AsUser returns ctx carrying a fresh user viewer for id.
func AsUser(ctx context.Context, id core.UserID, now time.Time) context.Context {
	return core.WithViewer(ctx, core.Viewer{ID: id, Kind: core.SessUser, AuthAt: now})
}

// ---- World ----

// World builds fixtures by label. Every fact goes to the in-memory deps and,
// when DB is set, to the database too (explicit high-range user IDs, ids.Named
// post IDs, then setval) so DB- and memory-backed tests agree.
type World struct {
	T  testing.TB
	D  *core.Deps
	DB *store.DB

	mu    sync.Mutex
	users map[string]core.UserID
	posts map[string]core.PostID
	next  core.UserID
}

// FirstFixtureID is where explicit fixture user IDs start (plan §3.1).
const FirstFixtureID core.UserID = 3_000_000_000

// NewWorld returns a world over d (and db, which may be nil).
func NewWorld(t testing.TB, d *core.Deps, db *store.DB) *World {
	return &World{T: t, D: d, DB: db, users: map[string]core.UserID{}, posts: map[string]core.PostID{}, next: FirstFixtureID}
}

// ID returns the user created for handle (fails the test if unknown).
func (w *World) ID(handle string) core.UserID {
	w.mu.Lock()
	defer w.mu.Unlock()
	id, ok := w.users[handle]
	if !ok {
		w.T.Fatalf("coretest: unknown user %q", handle)
	}
	return id
}

// PostID returns the post created for label.
func (w *World) PostID(label string) core.PostID {
	w.mu.Lock()
	defer w.mu.Unlock()
	id, ok := w.posts[label]
	if !ok {
		w.T.Fatalf("coretest: unknown post %q", label)
	}
	return id
}

// UserOpt adjusts a fixture user.
type UserOpt func(*core.UserMeta)

// InvitedBy sets the inviter (otherwise the user is genesis).
func InvitedBy(id core.UserID) UserOpt {
	return func(m *core.UserMeta) { m.InvitedBy, m.Genesis = id, false }
}

// NamedInChains opts the user in to being named in chains.
func NamedInChains() UserOpt { return func(m *core.UserMeta) { m.NameInChains = true } }

// User creates an active user with handle.
func (w *World) User(handle string, opts ...UserOpt) core.UserID {
	w.T.Helper()
	w.mu.Lock()
	id := w.next
	w.next++
	w.users[handle] = id
	w.mu.Unlock()
	m := core.UserMeta{ID: id, ExtID: ids.Named("user:" + handle), Handle: handle, Genesis: true, Status: core.StatusActive,
		CreatedAt: Epoch, Suggestible: true, NamedInLikes: true, Preset: core.PresetBalanced, Locale: "en",
		Languages: []string{"en"}}
	for _, o := range opts {
		o(&m)
	}
	if mu, ok := w.D.Users.(*coreref.MemUsers); ok {
		mu.Put(m)
	}
	if w.DB != nil {
		var inv *int64
		if m.InvitedBy != 0 {
			v := int64(m.InvitedBy)
			inv = &v
		}
		wid := sha256.Sum256([]byte("webauthn:" + handle))
		w.exec(`INSERT INTO users (id, ext_id, handle, handle_canon, invited_by, is_genesis, status, created_at,
			name_in_chains, webauthn_user_id) VALUES ($1, $2, $3, $4, $5, $6, 0, $7, $8, $9)`,
			int64(id), m.ExtID, handle, handle, inv, m.Genesis, Epoch, m.NameInChains, wid[:])
		w.exec("SELECT setval(pg_get_serial_sequence('users', 'id'), GREATEST((SELECT max(id) FROM users), 1))")
	}
	return id
}

// Friends makes a and b friends (friend level).
func (w *World) Friends(a, b string) {
	w.T.Helper()
	ia, ib := w.ID(a), w.ID(b)
	if w.DB != nil {
		h := sha256.Sum256([]byte("edge:" + a + ":" + b))
		for _, p := range [][2]core.UserID{{ia, ib}, {ib, ia}} {
			w.exec("INSERT INTO edges (user_id, friend_id, level, since, record_hash) VALUES ($1, $2, 1, $3, $4)",
				int64(p[0]), int64(p[1]), Epoch, h[:])
		}
	}
	w.D.Graph.AddEdge(ia, ib, core.LevelFriend, Epoch)
}

// Block records that a blocked b.
func (w *World) Block(a, b string) {
	w.T.Helper()
	ia, ib := w.ID(a), w.ID(b)
	if w.DB != nil {
		w.exec("INSERT INTO blocks (blocker, blocked, at) VALUES ($1, $2, $3)", int64(ia), int64(ib), Epoch)
	}
	w.D.Graph.SetBlock(ia, ib, true)
}

// PostOpt adjusts a fixture post.
type PostOpt func(*core.PostMeta, *[]core.LinkMeta)

// Audience sets the post audience.
func Audience(a core.Audience) PostOpt {
	return func(m *core.PostMeta, _ *[]core.LinkMeta) { m.Audience = a }
}

// At sets the creation time.
func At(t time.Time) PostOpt { return func(m *core.PostMeta, _ *[]core.LinkMeta) { m.CreatedAt = t } }

// Post creates a post by author with label (ID = ids.Named("post:"+label)).
// Parents are given as labels via ReplyTo.
func (w *World) Post(author, label, body string, opts ...PostOpt) core.PostID {
	w.T.Helper()
	id := ids.Named("post:" + label)
	m := core.PostMeta{ID: id, Author: w.ID(author), CreatedAt: w.D.Clock.Now(), Version: 1, Lang: "en"}
	var links []core.LinkMeta
	for _, o := range opts {
		o(&m, &links)
	}
	for i := range links {
		links[i].Child, links[i].ChildAuthor, links[i].CreatedAt = id, m.Author, m.CreatedAt
		if links[i].Kind == core.LinkRef {
			m.IsReply = true
		}
	}
	w.mu.Lock()
	w.posts[label] = id
	w.mu.Unlock()
	w.D.Posts.Put(m)
	if sb, ok := w.D.Posts.(interface{ SetBody(core.PostID, string) }); ok {
		sb.SetBody(id, body)
	}
	for _, l := range links {
		w.D.Posts.PutLink(l)
	}
	if w.DB != nil {
		h := sha256.Sum256([]byte("post:" + label))
		var aud *int16
		if m.Audience != core.AudConnected {
			v := int16(m.Audience)
			aud = &v
		}
		w.exec(`INSERT INTO posts (id, author_id, body, audience, ref_policy, lang, created_at, record_hash)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, int64(m.Author), body, aud, int16(m.RefPolicy), m.Lang, m.CreatedAt, h[:])
		for i, l := range links {
			w.exec(`INSERT INTO post_parents (child_id, parent_id, ord, kind, child_author, parent_author, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`, l.Child, l.Parent, i, int16(l.Kind), int64(l.ChildAuthor),
				int64(l.ParentAuthor), l.CreatedAt)
		}
	}
	return id
}

// ReplyTo adds a parent link (kind ref) to the post labelled parent.
func (w *World) ReplyTo(parent string) PostOpt {
	pid := w.PostID(parent)
	pm, _ := w.D.Posts.Meta(context.Background(), pid)
	return func(_ *core.PostMeta, ls *[]core.LinkMeta) {
		*ls = append(*ls, core.LinkMeta{Parent: pid, Kind: core.LinkRef, ParentAuthor: pm.Author})
	}
}

// Like records that user liked the post labelled label.
func (w *World) Like(user, label string) {
	w.T.Helper()
	uid, pid := w.ID(user), w.PostID(label)
	if me, ok := w.D.Engage.(*coreref.MemEngage); ok {
		me.SetLike(uid, pid, true)
	}
	if w.DB != nil {
		w.exec("INSERT INTO engagements (user_id, post_id, kind, at) VALUES ($1, $2, 1, $3)", int64(uid), pid, Epoch)
	}
}

func (w *World) exec(sql string, args ...any) {
	w.T.Helper()
	if _, err := w.DB.Q().Exec(context.Background(), sql, args...); err != nil {
		w.T.Fatalf("coretest: %v", err)
	}
}

// ---- FakePasskeys ----

// FakePasskeys is a software authenticator for Go tests: assertions are the JSON
// {"user":<id>,"purpose":"<purpose>","challenge":"<b64url>"} and verify when
// user and purpose match the viewer and the challenge was issued.
type FakePasskeys struct {
	mu         sync.Mutex
	challenges map[string]bool
}

var _ core.Passkeys = (*FakePasskeys)(nil)

type fakeAssertion struct {
	User      uint32 `json:"user"`
	Purpose   string `json:"purpose"`
	Challenge string `json:"challenge"`
}

// Assert builds an assertion body for v over challenge.
func (f *FakePasskeys) Assert(v core.UserID, purpose string, challenge []byte) []byte {
	b, _ := json.Marshal(fakeAssertion{User: uint32(v), Purpose: purpose, Challenge: base64.RawURLEncoding.EncodeToString(challenge)})
	return b
}

// BeginRegistration returns fake options.
func (f *FakePasskeys) BeginRegistration(context.Context, [32]byte, core.PendingUser) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// FinishRegistration returns a deterministic credential.
func (f *FakePasskeys) FinishRegistration(_ context.Context, sess [32]byte, _ []byte) (core.Credential, error) {
	return core.Credential{ID: sess[:16], PublicKey: sess[16:], Alg: -7, UV: true}, nil
}

// BindCredential succeeds.
func (f *FakePasskeys) BindCredential(context.Context, store.Q, core.UserID, core.Credential) error {
	return nil
}

// Upgrade returns a fake token.
func (f *FakePasskeys) Upgrade(_ context.Context, _ store.Q, _ [32]byte, user core.UserID, _ []byte) (string, error) {
	return fmt.Sprintf("fake-session-%d", user), nil
}

// BeginAssertion records the challenge.
func (f *FakePasskeys) BeginAssertion(_ context.Context, v core.Viewer, challenge []byte, purpose string) (json.RawMessage, error) {
	if v.ID == 0 {
		return nil, gate.ErrNotFound
	}
	f.mu.Lock()
	if f.challenges == nil {
		f.challenges = map[string]bool{}
	}
	c := base64.RawURLEncoding.EncodeToString(challenge)
	f.challenges[c] = true
	f.mu.Unlock()
	return json.Marshal(map[string]string{"challenge": c})
}

// VerifyAssertion checks a fake assertion (single use).
func (f *FakePasskeys) VerifyAssertion(_ context.Context, v core.Viewer, purpose string, body []byte) (core.Assertion, error) {
	var a fakeAssertion
	if err := json.Unmarshal(body, &a); err != nil || core.UserID(a.User) != v.ID || a.Purpose != purpose {
		return core.Assertion{}, gate.ErrNotFound
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.challenges[a.Challenge] {
		return core.Assertion{}, gate.ErrNotFound
	}
	delete(f.challenges, a.Challenge)
	return core.Assertion{User: v.ID, ClientDataJSON: body}, nil
}

// IssueEnrollment returns a fake token.
func (f *FakePasskeys) IssueEnrollment(_ context.Context, _ store.Q, user core.UserID, _ uint8, _ time.Time) (string, error) {
	return fmt.Sprintf("fake-enroll-%d", user), nil
}

// RevokeAll succeeds.
func (f *FakePasskeys) RevokeAll(context.Context, store.Q, core.UserID) error { return nil }

// NewPostID returns a fresh random post ID (helper).
func NewPostID() core.PostID { return uuid.Must(uuid.NewV7()) }
