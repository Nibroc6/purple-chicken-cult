// Package behavior runs the nightly behavioral-signal jobs (ARCHITECTURE §9(5),
// §17.4; plan P1-W06): posting cadence, cross-account similarity (MinHash
// within invite subtrees; bodies are never stored) and invite-tree clustering
// on shared networks. Each finding is an automation_signals row (aggregates
// only: counts, never content, never an address) and a moderation queue item
// (core.ModQueue) for human review. Signals never ban, suspend or step anyone
// up, and nothing of them is shown to members.
//
// Cross-account network correlation uses net_h only (HMAC of the /24 or /56
// under a daily salt, written by gate/signals/automation): no IP is ever at
// rest. signals.purge deletes every signal row after 30 days.
//
// Owner: P1-W06.
package behavior

import (
	"context"
	"encoding/json"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Jobs.
const (
	// JobNightly runs the cadence, similarity and invite-cluster jobs.
	JobNightly = "signals.nightly"
	// JobPurge deletes signal rows older than Retention.
	JobPurge = "signals.purge"
	// NightlyEvery and PurgeEvery are the job periods.
	NightlyEvery = 24 * time.Hour
	PurgeEvery   = 24 * time.Hour
	// Retention of automation_signals rows (every kind).
	Retention = 30 * 24 * time.Hour
)

// Signal kinds written by this module (automation_signals.kind).
const (
	KindCadence       = "cadence"
	KindSimilarity    = "similarity"
	KindInviteCluster = "invite_cluster"
)

// Thresholds. Tuned so that a person posting by hand never trips them: the
// write limits allow 30 posts a day, 10 an hour.
const (
	// CadenceWindow is the posting window of the cadence job.
	CadenceWindow = 24 * time.Hour
	// CadenceMinPosts posts in the window are needed before cadence is scored.
	CadenceMinPosts = 12
	// CadenceMinHours distinct UTC hours of posting is round-the-clock activity.
	CadenceMinHours = 16
	// CadenceMaxCV is the largest coefficient of variation of the gaps between
	// posts that counts as machine-regular.
	CadenceMaxCV = 0.15
	// SimilarityWindow is the posting window of the similarity job.
	SimilarityWindow = 7 * 24 * time.Hour
	// SimilarityPosts is how many recent posts of an account are sketched.
	SimilarityPosts = 50
	// SimilarityMin is the estimated Jaccard similarity of two accounts' posts
	// that links them.
	SimilarityMin = 0.7
	// AncestorDepth is how far up the invite tree two accounts may share an
	// ancestor to be compared ("within invite subtrees").
	AncestorDepth = 3
	// ClusterMin accounts make a similarity or invite cluster.
	ClusterMin = 3
	// NetWindow is how far back the invite-cluster job reads network hashes.
	NetWindow = 7 * 24 * time.Hour
)

// Service is the behavioral-signals module.
type Service struct{ d *core.Deps }

// New returns the service.
func New(d *core.Deps) *Service { return &Service{d: d} }

// Init registers signals.nightly and signals.purge.
func (s *Service) Init(context.Context) error {
	if s.d.Jobs != nil {
		s.d.Jobs.Handle(JobNightly, func(ctx context.Context, _ json.RawMessage) error {
			_, err := s.Nightly(ctx)
			return err
		})
		s.d.Jobs.Every(JobNightly, NightlyEvery)
		s.d.Jobs.Handle(JobPurge, func(ctx context.Context, _ json.RawMessage) error {
			_, err := s.Purge(ctx)
			return err
		})
		s.d.Jobs.Every(JobPurge, PurgeEvery)
	}
	return nil
}

// Findings counts what one nightly run queued for review.
type Findings struct {
	Cadence        int // accounts
	Similarity     int // clusters
	InviteClusters int // clusters
}

// Nightly runs the three jobs.
func (s *Service) Nightly(ctx context.Context) (Findings, error) {
	var (
		f   Findings
		err error
	)
	if f.Cadence, err = s.Cadence(ctx); err != nil {
		return f, err
	}
	if f.Similarity, err = s.Similarity(ctx); err != nil {
		return f, err
	}
	f.InviteClusters, err = s.InviteClusters(ctx)
	return f, err
}

// Purge deletes signal rows older than Retention (signals.purge).
func (s *Service) Purge(ctx context.Context) (int, error) {
	tag, err := s.d.DB.Q().Exec(ctx, "DELETE FROM automation_signals WHERE at < $1", s.d.Clock.Now().Add(-Retention))
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// active reports whether u is an active member (signals skip everyone else).
func (s *Service) active(ctx context.Context, u core.UserID) bool {
	m, err := s.d.Users.Get(ctx, u)
	return err == nil && m.Status == core.StatusActive
}

// windowPosts returns the live window posts created after since, per author,
// newest first.
func (s *Service) windowPosts(since time.Time) map[core.UserID][]core.PostMeta {
	out := map[core.UserID][]core.PostMeta{}
	s.d.Posts.WindowScan(func(m core.PostMeta) bool {
		if m.CreatedAt.Before(since) {
			return false
		}
		if !m.Deleted {
			out[m.Author] = append(out[m.Author], m)
		}
		return true
	})
	return out
}

// finding is one account's signal and the queue item it feeds.
type finding struct {
	user    core.UserID
	kind    string
	score   float64
	data    map[string]int
	subject core.UserID // the queue item's subject
	source  uint8
	summary map[string]int
}

// record writes the signal rows and the queue items of fs in one transaction.
func (s *Service) record(ctx context.Context, fs []finding) error {
	if len(fs) == 0 {
		return nil
	}
	now := s.d.Clock.Now()
	return s.d.DB.Tx(ctx, func(tx pgx.Tx) error {
		queued := map[[2]int64]bool{}
		for _, f := range fs {
			data, err := json.Marshal(f.data)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO automation_signals (user_id, kind, score, data, at) VALUES ($1, $2, $3, $4, $5)`,
				int64(f.user), f.kind, float32(f.score), data, now); err != nil {
				return err
			}
			key := [2]int64{int64(f.subject), int64(f.source)}
			if queued[key] {
				continue
			}
			queued[key] = true
			if err := s.enqueue(ctx, tx, f); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Service) enqueue(ctx context.Context, tx store.Q, f finding) error {
	if s.d.ModQueue == nil {
		return nil
	}
	return s.d.ModQueue.Enqueue(ctx, tx, core.ModQueueItem{Source: f.source, Priority: 2, SubjectUser: f.subject,
		Summary: f.summary})
}

// ---- cadence ----

// Cadence scores each account's posting rhythm over the last CadenceWindow:
// at least CadenceMinPosts posts spread over CadenceMinHours distinct hours
// (round the clock) or with nearly identical gaps (coefficient of variation ≤
// CadenceMaxCV). It returns the number of accounts flagged.
func (s *Service) Cadence(ctx context.Context) (int, error) {
	now := s.d.Clock.Now()
	var fs []finding
	for u, posts := range s.windowPosts(now.Add(-CadenceWindow)) {
		if len(posts) < CadenceMinPosts || !s.active(ctx, u) {
			continue
		}
		hours := map[int]bool{}
		times := make([]time.Time, 0, len(posts))
		for _, p := range posts {
			hours[p.CreatedAt.UTC().Hour()] = true
			times = append(times, p.CreatedAt)
		}
		slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
		cv := gapCV(times)
		if len(hours) < CadenceMinHours && cv > CadenceMaxCV {
			continue
		}
		data := map[string]int{"posts": len(posts), "hours": len(hours), "cv_milli": int(math.Round(min(cv, 9.999) * 1000))}
		fs = append(fs, finding{user: u, kind: KindCadence, score: float64(len(posts)) / CadenceMinPosts, data: data,
			subject: u, source: core.ModSrcSignalCluster,
			summary: map[string]int{KindCadence: 1, "posts": len(posts), "hours": len(hours)}})
	}
	sortFindings(fs)
	return len(fs), s.record(ctx, fs)
}

// gapCV is the coefficient of variation of the gaps between sorted times.
func gapCV(ts []time.Time) float64 {
	if len(ts) < 3 {
		return math.Inf(1)
	}
	gaps := make([]float64, 0, len(ts)-1)
	var sum float64
	for i := 1; i < len(ts); i++ {
		g := ts[i].Sub(ts[i-1]).Seconds()
		gaps = append(gaps, g)
		sum += g
	}
	mean := sum / float64(len(gaps))
	if mean <= 0 {
		return 0 // a burst at one instant is as regular as it gets
	}
	var v float64
	for _, g := range gaps {
		v += (g - mean) * (g - mean)
	}
	return math.Sqrt(v/float64(len(gaps))) / mean
}

func sortFindings(fs []finding) {
	slices.SortFunc(fs, func(a, b finding) int {
		if a.subject != b.subject {
			return int(a.subject) - int(b.subject)
		}
		return int(a.user) - int(b.user)
	})
}

// ---- similarity ----

// ancestors returns u's inviters up to AncestorDepth levels.
func (s *Service) ancestors(u core.UserID) []core.UserID {
	var out []core.UserID
	for range AncestorDepth {
		inv, ok := s.d.Users.InviterOf(u)
		if !ok || inv == 0 {
			break
		}
		out = append(out, inv)
		u = inv
	}
	return out
}

// Similarity sketches every active account's recent posts (MinHash over word
// shingles; the text is read from the post index and dropped right away) and
// links two accounts that share an ancestor within AncestorDepth invite levels
// and whose sketches estimate a Jaccard similarity ≥ SimilarityMin (LSH
// banding finds the candidates). Linked groups of ≥ ClusterMin accounts are
// queued, one item per cluster. It returns the number of clusters.
func (s *Service) Similarity(ctx context.Context) (int, error) {
	now := s.d.Clock.Now()
	sketches := map[core.UserID]Sketch{}
	for u, posts := range s.windowPosts(now.Add(-SimilarityWindow)) {
		if !s.active(ctx, u) {
			continue
		}
		if len(posts) > SimilarityPosts {
			posts = posts[:SimilarityPosts]
		}
		ids := make([]core.PostID, len(posts))
		for i, p := range posts {
			ids[i] = p.ID
		}
		bodies := s.d.Posts.Bodies(ctx, ids)
		texts := make([]string, 0, len(bodies))
		for _, id := range ids {
			texts = append(texts, bodies[id])
		}
		if sk, ok := SketchOf(texts); ok {
			sketches[u] = sk
		}
	}
	// Candidate pairs: same (ancestor, band, band key) bucket.
	type bucket struct {
		anc  core.UserID
		band int
		key  uint64
	}
	buckets := map[bucket][]core.UserID{}
	for u, sk := range sketches {
		for _, a := range s.ancestors(u) {
			for b := range Bands {
				k := bucket{a, b, sk.band(b)}
				buckets[k] = append(buckets[k], u)
			}
		}
	}
	uf := newUnionFind()
	best := map[core.UserID]float64{}
	seen := map[[2]core.UserID]bool{}
	for _, us := range buckets {
		for i := range us {
			for j := i + 1; j < len(us); j++ {
				a, b := min(us[i], us[j]), max(us[i], us[j])
				if a == b || seen[[2]core.UserID{a, b}] {
					continue
				}
				seen[[2]core.UserID{a, b}] = true
				if jc := Jaccard(sketches[a], sketches[b]); jc >= SimilarityMin {
					uf.union(a, b)
					best[a], best[b] = max(best[a], jc), max(best[b], jc)
				}
			}
		}
	}
	var fs []finding
	clusters := 0
	for _, members := range uf.groups() {
		if len(members) < ClusterMin {
			continue
		}
		clusters++
		subject := members[0]
		for _, u := range members {
			fs = append(fs, finding{user: u, kind: KindSimilarity, score: best[u],
				data:    map[string]int{"accounts": len(members), "jaccard_pct": int(math.Round(best[u] * 100))},
				subject: subject, source: core.ModSrcSignalCluster,
				summary: map[string]int{KindSimilarity: 1, "accounts": len(members)}})
		}
	}
	sortFindings(fs)
	return clusters, s.record(ctx, fs)
}

// ---- invite clusters ----

// InviteClusters groups the network hashes (net_h) recorded for accounts in
// the last NetWindow by the accounts' inviter: ≥ ClusterMin invitees of one
// inviter on one network make a cluster, queued as one invite_cluster item.
// The hash is only compared, never copied into a new row. It returns the
// number of clusters.
func (s *Service) InviteClusters(ctx context.Context) (int, error) {
	now := s.d.Clock.Now()
	rows, err := s.d.DB.Q().Query(ctx, `SELECT DISTINCT user_id, net_h FROM automation_signals
		WHERE net_h IS NOT NULL AND at >= $1`, now.Add(-NetWindow))
	if err != nil {
		return 0, err
	}
	type key struct {
		inviter core.UserID
		net     string
	}
	groups := map[key]map[core.UserID]bool{}
	for rows.Next() {
		var (
			id  int64
			net []byte
		)
		if err := rows.Scan(&id, &net); err != nil {
			rows.Close()
			return 0, err
		}
		u := core.UserID(id) //nolint:gosec // users.id < 2^32 (DB CHECK)
		inv, ok := s.d.Users.InviterOf(u)
		if !ok || inv == 0 {
			continue
		}
		k := key{inv, string(net)}
		if groups[k] == nil {
			groups[k] = map[core.UserID]bool{}
		}
		groups[k][u] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	uf := newUnionFind()
	for _, g := range groups {
		var members []core.UserID
		for u := range g {
			if s.active(ctx, u) {
				members = append(members, u)
			}
		}
		if len(members) < ClusterMin {
			continue
		}
		for _, u := range members[1:] {
			uf.union(members[0], u)
		}
	}
	var fs []finding
	clusters := 0
	for _, members := range uf.groups() {
		if len(members) < ClusterMin {
			continue
		}
		clusters++
		for _, u := range members {
			fs = append(fs, finding{user: u, kind: KindInviteCluster, score: float64(len(members)) / ClusterMin,
				data: map[string]int{"accounts": len(members)}, subject: members[0], source: core.ModSrcInviteCluster,
				summary: map[string]int{KindInviteCluster: 1, "accounts": len(members), "shared_network": 1}})
		}
	}
	sortFindings(fs)
	return clusters, s.record(ctx, fs)
}

// ---- union-find over user IDs ----

type unionFind struct{ parent map[core.UserID]core.UserID }

func newUnionFind() *unionFind { return &unionFind{parent: map[core.UserID]core.UserID{}} }

func (u *unionFind) find(x core.UserID) core.UserID {
	p, ok := u.parent[x]
	if !ok {
		u.parent[x] = x
		return x
	}
	if p == x {
		return x
	}
	r := u.find(p)
	u.parent[x] = r
	return r
}

func (u *unionFind) union(a, b core.UserID) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.parent[max(ra, rb)] = min(ra, rb)
	}
}

// groups returns every set, each sorted ascending.
func (u *unionFind) groups() [][]core.UserID {
	m := map[core.UserID][]core.UserID{}
	for x := range u.parent {
		r := u.find(x)
		m[r] = append(m[r], x)
	}
	out := make([][]core.UserID, 0, len(m))
	for _, g := range m {
		slices.Sort(g)
		out = append(out, g)
	}
	slices.SortFunc(out, func(a, b []core.UserID) int { return int(a[0]) - int(b[0]) })
	return out
}
