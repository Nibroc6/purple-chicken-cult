// Package hstest plays the two devices of an in-person handshake (protocol
// v2, see package invites) for Go tests: the initiator's device (ephemeral
// key E_i, client-rendered frames from the frame key, the decode report, the
// dual-QR frame) and the claiming device (ephemeral key E_v, the separate
// ECDSA signing key, the claim, signed join calls, kmac and the sound bits).
// It computes exactly what the browser code in web/src/features/{invites,join}
// computes, so tests drive the real server checks end to end.
//
// Owner: P1-W02. Never imported by production code.
package hstest

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

var b64 = base64.RawURLEncoding

// ErrPayload is returned for a payload that is not a v2 frame.
var ErrPayload = errors.New("hstest: not a pcc2 frame")

func be64(n int64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(n)) //nolint:gosec // frame indexes are never negative
	return b[:]
}

func mac(key []byte, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write(p)
	}
	return m.Sum(nil)
}

// FrameMAC is HMAC(frame_key, "pcc-frame-v2" ‖ hs ‖ E_i ‖ n)[:10].
func FrameMAC(key []byte, hs uuid.UUID, ei []byte, n int64) []byte {
	return mac(key, []byte("pcc-frame-v2"), hs[:], ei, be64(n))[:10]
}

// Frame is the QR payload of frame n.
func Frame(key []byte, hs uuid.UUID, ei []byte, n int64) string {
	return fmt.Sprintf("pcc2.%s.%s.%d.%s", b64.EncodeToString(hs[:]), b64.EncodeToString(ei), n,
		b64.EncodeToString(FrameMAC(key, hs, ei, n)))
}

// Parse splits a v2 frame into hs, E_i and n.
func Parse(payload string) (uuid.UUID, []byte, int64, error) {
	p := strings.Split(payload, ".")
	if len(p) != 5 || p[0] != "pcc2" {
		return uuid.Nil, nil, 0, ErrPayload
	}
	raw, err := b64.DecodeString(p[1])
	if err != nil {
		return uuid.Nil, nil, 0, ErrPayload
	}
	hs, err := uuid.FromBytes(raw)
	if err != nil {
		return uuid.Nil, nil, 0, ErrPayload
	}
	ei, err := b64.DecodeString(p[2])
	if err != nil {
		return uuid.Nil, nil, 0, ErrPayload
	}
	n, err := strconv.ParseInt(p[3], 10, 64)
	if err != nil {
		return uuid.Nil, nil, 0, ErrPayload
	}
	return hs, ei, n, nil
}

// Kmac is HKDF-SHA256(S, salt = hs, info "pcc-hs-v2 kmac", 32 bytes).
func Kmac(shared []byte, hs uuid.UUID) []byte {
	k, err := hkdf.Key(sha256.New, shared, hs[:], "pcc-hs-v2 kmac", 32)
	if err != nil {
		panic(err) // fixed parameters: cannot fail
	}
	return k
}

// Transcript is the MAC input of one sound attempt.
func Transcript(hs uuid.UUID, ei, ev []byte, n int64, attempt int) []byte {
	t := append([]byte("pcc-hs-v2"), hs[:]...)
	t = append(t, ei...)
	t = append(t, ev...)
	t = append(t, be64(n)...)
	return append(t, byte(attempt)) //nolint:gosec // attempts ≤ 5
}

// Bits are the ultrasonic (4 B) and jingle (3 B) bits of a transcript.
func Bits(kmac, transcript []byte) (ultra, jingle []byte) {
	return mac(kmac, transcript)[:4], mac(kmac, transcript, []byte("jingle"))[:3]
}

// DualFrame is the dual-QR payload of frame n under kmac.
func DualFrame(kmac []byte, hs uuid.UUID, n int64) string {
	return fmt.Sprintf("pcc2d.%s.%d.%s", b64.EncodeToString(hs[:]), n,
		b64.EncodeToString(mac(kmac, []byte("pcc-dual-v2"), hs[:], be64(n))[:10]))
}

// Initiator is the device that shows the code and listens.
type Initiator struct {
	FrameKey []byte
	Priv     *ecdh.PrivateKey
	HS       uuid.UUID // learned from the server's first frame (Sync)
	N        int64     // that frame's index
}

// NewInitiator makes the device for a HandshakeView's frame_key.
func NewInitiator(frameKey string) (*Initiator, error) {
	k, err := b64.DecodeString(frameKey)
	if err != nil {
		return nil, err
	}
	p, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Initiator{FrameKey: k, Priv: p}, nil
}

// EI is E_i, base64url.
func (i *Initiator) EI() string { return b64.EncodeToString(i.Priv.PublicKey().Bytes()) }

// Sync takes hs and the frame counter from the server-rendered frame.
func (i *Initiator) Sync(payload string) error {
	hs, _, n, err := Parse(payload)
	i.HS, i.N = hs, n
	return err
}

// Frame renders frame n.
func (i *Initiator) Frame(n int64) string {
	return Frame(i.FrameKey, i.HS, i.Priv.PublicKey().Bytes(), n)
}

// Kmac derives kmac from the relayed E_v (base64url).
func (i *Initiator) Kmac(ev string) ([]byte, error) {
	raw, err := b64.DecodeString(ev)
	if err != nil {
		return nil, err
	}
	pub, err := ecdh.P256().NewPublicKey(raw)
	if err != nil {
		return nil, err
	}
	s, err := i.Priv.ECDH(pub)
	if err != nil {
		return nil, err
	}
	return Kmac(s, i.HS), nil
}

// Commit is sha256(kmac), base64url.
func Commit(kmac []byte) string {
	h := sha256.Sum256(kmac)
	return b64.EncodeToString(h[:])
}

// Claimer is the device that scans, plays and signs join calls.
type Claimer struct {
	ECDH    *ecdh.PrivateKey
	Sig     *ecdsa.PrivateKey
	Payload string
	HS      uuid.UUID
	EI      []byte
	N       int64
}

// NewClaimer makes a claiming device.
func NewClaimer() *Claimer {
	p, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	return &Claimer{ECDH: p, Sig: k}
}

// ClaimBody is the JSON of POST /api/join/claim.
type ClaimBody struct {
	Payload string `json:"payload"`
	EV      string `json:"e_v"`
	SigPub  string `json:"sig_pub"`
	Proof   string `json:"proof"`
}

// EV is E_v, base64url.
func (c *Claimer) EV() string { return b64.EncodeToString(c.ECDH.PublicKey().Bytes()) }

func (c *Claimer) sigPub() []byte {
	b, err := c.Sig.PublicKey.Bytes()
	if err != nil {
		panic(err)
	}
	return b
}

// sign is a P1363 ECDSA P-256 SHA-256 signature (what WebCrypto produces).
func (c *Claimer) sign(msg []byte) string {
	d := sha256.Sum256(msg)
	r, s, err := ecdsa.Sign(rand.Reader, c.Sig, d[:])
	if err != nil {
		panic(err)
	}
	out := make([]byte, 64)
	r.FillBytes(out[:32])
	s.FillBytes(out[32:])
	return b64.EncodeToString(out)
}

// Claim scans payload and returns the claim body (with a valid proof).
func (c *Claimer) Claim(payload string) ClaimBody {
	c.Payload = payload
	if hs, ei, n, err := Parse(payload); err == nil {
		c.HS, c.EI, c.N = hs, ei, n
	}
	ev := c.ECDH.PublicKey().Bytes()
	return ClaimBody{Payload: payload, EV: c.EV(), SigPub: b64.EncodeToString(c.sigPub()),
		Proof: c.sign(append(append([]byte("pcc-join-claim-v1"), payload...), ev...))}
}

// Call signs a later join call: "pcc-join-v1" ‖ hs ‖ sha256(payload) ‖ call.
func (c *Claimer) Call(call string) string {
	ch := sha256.Sum256([]byte(c.Payload))
	m := append([]byte("pcc-join-v1"), c.HS[:]...)
	m = append(m, ch[:]...)
	return c.sign(append(m, call...))
}

// Kmac derives kmac from the scanned E_i.
func (c *Claimer) Kmac() []byte {
	pub, err := ecdh.P256().NewPublicKey(c.EI)
	if err != nil {
		panic(err)
	}
	s, err := c.ECDH.ECDH(pub)
	if err != nil {
		panic(err)
	}
	return Kmac(s, c.HS)
}

// KmacB64 is Kmac, base64url.
func (c *Claimer) KmacB64() string { return b64.EncodeToString(c.Kmac()) }

// Bits are the base64url bits the device plays in attempt.
func (c *Claimer) Bits(attempt int) (ultra, jingle string) {
	u, j := Bits(c.Kmac(), Transcript(c.HS, c.EI, c.ECDH.PublicKey().Bytes(), c.N, attempt))
	return b64.EncodeToString(u), b64.EncodeToString(j)
}
