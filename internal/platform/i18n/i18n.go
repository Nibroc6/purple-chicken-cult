// Package i18n renders the few server-side strings (Web Push and other out-of-app
// messages, ARCHITECTURE §15.4) from ICU MessageFormat catalogs embedded under
// catalogs/<locale>/server.json, in the recipient's ui_locale with fallback "en".
// API responses never carry prose: they carry codes the client localizes.
//
// Owner: P1-W05 (the scaffold ships a working loader).
package i18n

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/kaptinlin/messageformat-go/mf1"
)

//go:embed catalogs
var catalogFS embed.FS

// Fallback is the locale used when a key or locale is missing.
const Fallback = "en"

// ErrUnknownKey is returned for a key missing from every candidate catalog.
var ErrUnknownKey = errors.New("i18n: unknown key")

// Catalog holds every locale's server messages.
type Catalog struct {
	msgs     map[string]map[string]string // locale → key → ICU source
	mu       sync.Mutex
	compiled map[string]*mf1.CompiledMessage // locale + "\x00" + key
}

// Load reads every catalogs/<locale>/server.json from fsys.
func Load(fsys fs.FS) (*Catalog, error) {
	c := &Catalog{msgs: map[string]map[string]string{}, compiled: map[string]*mf1.CompiledMessage{}}
	err := fs.WalkDir(fsys, "catalogs", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Base(p) != "server.json" {
			return err
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			return fmt.Errorf("i18n: %s: %w", p, err)
		}
		c.msgs[path.Base(path.Dir(p))] = m
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, ok := c.msgs[Fallback]; !ok {
		return nil, errors.New("i18n: fallback catalog missing")
	}
	return c, nil
}

var (
	defaultOnce sync.Once
	defaultCat  *Catalog
	defaultErr  error
)

// Default returns the embedded catalogs.
func Default() (*Catalog, error) {
	defaultOnce.Do(func() { defaultCat, defaultErr = Load(catalogFS) })
	return defaultCat, defaultErr
}

// Locales lists the loaded locales.
func (c *Catalog) Locales() []string {
	out := make([]string, 0, len(c.msgs))
	for l := range c.msgs {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

// Keys lists the fallback catalog's keys.
func (c *Catalog) Keys() []string {
	out := make([]string, 0, len(c.msgs[Fallback]))
	for k := range c.msgs[Fallback] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func candidates(locale string) []string {
	out := []string{}
	if locale != "" {
		out = append(out, locale)
		if base, _, ok := strings.Cut(locale, "-"); ok {
			out = append(out, base)
		}
	}
	return append(out, Fallback)
}

// Format renders key in locale (falling back to the base language, then "en").
func (c *Catalog) Format(locale, key string, args map[string]any) (string, error) {
	for _, l := range candidates(locale) {
		src, ok := c.msgs[l][key]
		if !ok {
			continue
		}
		msg, err := c.compile(l, key, src)
		if err != nil {
			return "", err
		}
		return msg.Format(args)
	}
	return "", fmt.Errorf("%w: %s", ErrUnknownKey, key)
}

func (c *Catalog) compile(locale, key, src string) (*mf1.CompiledMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ck := locale + "\x00" + key
	if m, ok := c.compiled[ck]; ok {
		return m, nil
	}
	f, err := mf1.New(locale, nil)
	if err != nil {
		if f, err = mf1.New(Fallback, nil); err != nil {
			return nil, err
		}
	}
	m, err := f.Compile(src)
	if err != nil {
		return nil, fmt.Errorf("i18n: %s/%s: %w", locale, key, err)
	}
	c.compiled[ck] = m
	return m, nil
}

// Format renders key with the embedded catalogs.
func Format(locale, key string, args map[string]any) (string, error) {
	c, err := Default()
	if err != nil {
		return "", err
	}
	return c.Format(locale, key, args)
}
