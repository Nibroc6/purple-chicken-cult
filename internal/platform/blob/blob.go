// Package blob is the object-store abstraction for media (ARCHITECTURE §6): a
// filesystem driver for dev/test and an S3-compatible driver (Cloudflare R2, private
// bucket, r2.dev access off) for production. Blob keys are random, never content
// hashes; signed per-viewer URLs are built by gate/media, not here.
//
// Owner: P1-W04 (the scaffold ships working fs and s3 drivers).
package blob

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/Nibroc6/purple-chicken-cult/internal/platform/config"
)

// Store stores immutable blobs by key.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, mime string) error
	Get(ctx context.Context, key string) (io.ReadCloser, int64, string, error)
	Delete(ctx context.Context, key string) error
	Name() string
}

// Errors.
var (
	ErrNotFound     = errors.New("blob: not found")
	ErrDisabled     = errors.New("blob: store not configured")
	ErrBadKey       = errors.New("blob: invalid key")
	errFSNotAllowed = errors.New("blob: fs driver in prod requires PCC_ALLOW_FS_BLOB=1 and a real scanner")
)

var keyRE = regexp.MustCompile(`^[a-z2-7]{16,64}$`) // base32 (lowercase, no padding)

// ValidKey reports whether key has the random base32 shape.
func ValidKey(key string) bool { return keyRE.MatchString(key) }

// New builds the store for b. In prod the fs driver requires PCC_ALLOW_FS_BLOB=1
// and a real (photodna) scanner, and logs a WARN on every start. An unconfigured
// store returns ErrDisabled (media then reports feature_disabled).
func New(c *config.Config, b config.BlobConfig, log *slog.Logger) (Store, error) {
	if !b.Configured() {
		return nil, ErrDisabled
	}
	switch b.Driver {
	case config.BlobFS:
		if c.Prod() {
			if !c.AllowFSBlob || (c.Scanner.Driver != config.ScannerPhotoDNA && !c.UnscannedMedia()) {
				return nil, errFSNotAllowed
			}
			if log != nil {
				log.Warn("fs blob store in prod", "feature", "media")
			}
		}
		return NewFS(b.FSDir)
	case config.BlobS3:
		return NewS3(b)
	}
	return nil, ErrDisabled
}

// ---- filesystem driver ----

// FS stores blobs as files under a directory.
type FS struct{ dir string }

// NewFS returns a filesystem store rooted at dir (created if missing).
func NewFS(dir string) (*FS, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &FS{dir: dir}, nil
}

// Name identifies the driver.
func (f *FS) Name() string { return "fs" }

func (f *FS) path(key string) (string, error) {
	if !ValidKey(key) {
		return "", ErrBadKey
	}
	return filepath.Join(f.dir, key[:2], key), nil
}

// Put writes atomically (temp file + rename).
func (f *FS) Put(_ context.Context, key string, r io.Reader, size int64, _ string) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	n, err := io.Copy(tmp, io.LimitReader(r, size+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != size {
		err = fmt.Errorf("blob: size mismatch (%d != %d)", n, size)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Get opens a blob. The mime type is not stored by the fs driver (media_variants has it).
func (f *FS) Get(_ context.Context, key string) (io.ReadCloser, int64, string, error) {
	p, err := f.path(key)
	if err != nil {
		return nil, 0, "", err
	}
	fh, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, "", ErrNotFound
	}
	if err != nil {
		return nil, 0, "", err
	}
	st, err := fh.Stat()
	if err != nil {
		_ = fh.Close()
		return nil, 0, "", err
	}
	return fh, st.Size(), "", nil
}

// Delete removes a blob (missing is not an error).
func (f *FS) Delete(_ context.Context, key string) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ---- S3 / R2 driver ----

// S3 stores blobs in an S3-compatible private bucket.
type S3 struct {
	c      *minio.Client
	bucket string
}

// NewS3 returns an S3-compatible store.
func NewS3(b config.BlobConfig) (*S3, error) {
	c, err := minio.New(b.S3Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(b.S3AccessKey, b.S3SecretKey, ""),
		Secure: b.S3UseSSL,
		Region: b.S3Region,
	})
	if err != nil {
		return nil, err
	}
	return &S3{c: c, bucket: b.S3Bucket}, nil
}

// Name identifies the driver.
func (s *S3) Name() string { return "s3" }

// Put uploads a blob.
func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, mime string) error {
	if !ValidKey(key) {
		return ErrBadKey
	}
	_, err := s.c.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{ContentType: mime})
	return err
}

// Get downloads a blob.
func (s *S3) Get(ctx context.Context, key string) (io.ReadCloser, int64, string, error) {
	if !ValidKey(key) {
		return nil, 0, "", ErrBadKey
	}
	o, err := s.c.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, "", err
	}
	st, err := o.Stat()
	if err != nil {
		_ = o.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, 0, "", ErrNotFound
		}
		return nil, 0, "", err
	}
	return o, st.Size, st.ContentType, nil
}

// Delete removes a blob.
func (s *S3) Delete(ctx context.Context, key string) error {
	if !ValidKey(key) {
		return ErrBadKey
	}
	return s.c.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{})
}
