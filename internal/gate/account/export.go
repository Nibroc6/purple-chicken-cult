package account

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
	"github.com/Nibroc6/purple-chicken-cult/internal/platform/sigs"
	"github.com/Nibroc6/purple-chicken-cult/internal/store"
)

// Export file names (shared with records.VerifyExport).
const (
	fileRecords     = "records.jsonl"
	fileKeys        = "keys.json"
	filePosts       = "posts.jsonl"
	fileCredentials = "credentials.jsonl"
	fileManifest    = "manifest.json"
)

// ExportFormat identifies the export layout in manifest.json.
const ExportFormat = "pcc-export"

// ExportVersion is the export layout version.
const ExportVersion = 1

// Manifest is manifest.json.
type Manifest struct {
	Format     string         `json:"format"`
	Version    int            `json:"version"`
	ExportedAt sigs.Time      `json:"exported_at"`
	User       ManifestUser   `json:"user"`
	Counts     map[string]int `json:"counts"`
	Files      []string       `json:"files"`
}

// ManifestUser names the exporting account.
type ManifestUser struct {
	ID     uuid.UUID `json:"id"` // ext_id: how records name users
	Handle string    `json:"handle"`
}

// postLine is one posts.jsonl line: a live post body with the salt that opens
// its signed body_commit. Deleted posts have neither (security #25).
type postLine struct {
	ID      uuid.UUID  `json:"id"`
	Version int        `json:"version"`
	Body    string     `json:"body"`
	Salt    sigs.Bytes `json:"salt"`
}

// credentialLine is one credentials.jsonl line (public parts only).
type credentialLine struct {
	ID        sigs.Bytes `json:"id"`
	PublicKey sigs.Bytes `json:"public_key"` // COSE
	Alg       int        `json:"alg"`
	Name      string     `json:"name"`
	CreatedAt sigs.Time  `json:"created_at"`
}

func (s *Service) writeExport(ctx context.Context, user core.UserID, w io.Writer) error {
	ex := s.d.RecordExporter
	if ex == nil {
		return errNoDep
	}
	tx, err := s.d.DB.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		ext    uuid.UUID
		handle string
	)
	if err := tx.QueryRow(ctx, "SELECT ext_id, handle FROM users WHERE id = $1", int64(user)).Scan(&ext, &handle); err != nil {
		return err
	}
	recs, keys, err := ex.ExportRecords(ctx, tx, user)
	if err != nil {
		return err
	}
	posts, nPosts, err := exportPosts(ctx, tx, user)
	if err != nil {
		return err
	}
	creds, nCreds, err := exportCredentials(ctx, tx, user)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	files := []string{fileManifest, fileRecords, fileKeys, filePosts, fileCredentials}
	man := Manifest{Format: ExportFormat, Version: ExportVersion, ExportedAt: sigs.T(s.d.Clock.Now()),
		User: ManifestUser{ID: ext, Handle: handle}, Files: files,
		Counts: map[string]int{"records": bytes.Count(recs, []byte{'\n'}), "posts": nPosts, "credentials": nCreds}}
	manJSON, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}

	zw := zip.NewWriter(w)
	mod := s.d.Clock.Now().UTC().Truncate(time.Second)
	for _, f := range []struct {
		name string
		data []byte
	}{{fileManifest, manJSON}, {fileRecords, recs}, {fileKeys, keys}, {filePosts, posts}, {fileCredentials, creds}} {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: mod})
		if err != nil {
			return err
		}
		if _, err := fw.Write(f.data); err != nil {
			return err
		}
	}
	return zw.Close()
}

func jsonl() (*bytes.Buffer, *json.Encoder) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	return &buf, enc
}

func exportPosts(ctx context.Context, q store.Q, user core.UserID) ([]byte, int, error) {
	rows, err := q.Query(ctx, `SELECT id, version, body, body_salt FROM posts
		WHERE author_id = $1 AND deleted_at IS NULL AND body IS NOT NULL AND body_salt IS NOT NULL
		ORDER BY created_at, id`, int64(user))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	buf, enc := jsonl()
	n := 0
	for rows.Next() {
		var p postLine
		var salt []byte
		if err := rows.Scan(&p.ID, &p.Version, &p.Body, &salt); err != nil {
			return nil, 0, err
		}
		p.Salt = salt
		if err := enc.Encode(p); err != nil {
			return nil, 0, err
		}
		n++
	}
	return buf.Bytes(), n, rows.Err()
}

func exportCredentials(ctx context.Context, q store.Q, user core.UserID) ([]byte, int, error) {
	rows, err := q.Query(ctx, `SELECT id, public_key, alg, name, created_at FROM webauthn_credentials
		WHERE user_id = $1 ORDER BY created_at, id`, int64(user))
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	buf, enc := jsonl()
	n := 0
	for rows.Next() {
		var (
			c       credentialLine
			id, pub []byte
			at      time.Time
		)
		if err := rows.Scan(&id, &pub, &c.Alg, &c.Name, &at); err != nil {
			return nil, 0, err
		}
		c.ID, c.PublicKey, c.CreatedAt = id, pub, sigs.T(at)
		if err := enc.Encode(c); err != nil {
			return nil, 0, err
		}
		n++
	}
	return buf.Bytes(), n, rows.Err()
}
