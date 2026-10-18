package blob

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Mem keeps blobs in memory (tests, and the media service's own unit tests of
// "nothing was stored anywhere"). It is production-safe but never configured.
type Mem struct {
	name string

	mu    sync.Mutex
	blobs map[string]memBlob
	// FailPut makes every Put fail (tests of partial-failure clean-up).
	FailPut bool
}

type memBlob struct {
	b    []byte
	mime string
}

// NewMem returns an empty in-memory store called name.
func NewMem(name string) *Mem { return &Mem{name: name, blobs: map[string]memBlob{}} }

// Name identifies the store.
func (m *Mem) Name() string { return m.name }

// Put stores a copy of r's size bytes.
func (m *Mem) Put(_ context.Context, key string, r io.Reader, size int64, mime string) error {
	if !ValidKey(key) {
		return ErrBadKey
	}
	b, err := io.ReadAll(io.LimitReader(r, size+1))
	if err != nil {
		return err
	}
	if int64(len(b)) != size {
		return fmt.Errorf("blob: size mismatch (%d != %d)", len(b), size)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.FailPut {
		return fmt.Errorf("blob: %s: put refused", m.name)
	}
	m.blobs[key] = memBlob{b: b, mime: mime}
	return nil
}

// Get returns a blob.
func (m *Mem) Get(_ context.Context, key string) (io.ReadCloser, int64, string, error) {
	if !ValidKey(key) {
		return nil, 0, "", ErrBadKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blobs[key]
	if !ok {
		return nil, 0, "", ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b.b)), int64(len(b.b)), b.mime, nil
}

// Delete removes a blob (missing is not an error).
func (m *Mem) Delete(_ context.Context, key string) error {
	if !ValidKey(key) {
		return ErrBadKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.blobs, key)
	return nil
}

// Keys lists the stored keys (sorted).
func (m *Mem) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.blobs))
	for k := range m.blobs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Bytes returns a copy of the blob at key.
func (m *Mem) Bytes(key string) ([]byte, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blobs[key]
	return bytes.Clone(b.b), ok
}
