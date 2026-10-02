package i18n

import (
	"errors"
	"testing"
)

func TestFormat(t *testing.T) {
	got, err := Format("en", "push.post_referenced", map[string]any{"count": 5})
	if err != nil {
		t.Fatal(err)
	}
	if got != "5 new posts reference yours" {
		t.Fatalf("got %q", got)
	}
	one, _ := Format("en", "push.post_referenced", map[string]any{"count": 1})
	if one != "1 new post references yours" {
		t.Fatalf("plural one: %q", one)
	}
}

func TestFallback(t *testing.T) {
	got, err := Format("de-AT", "push.generic", nil)
	if err != nil || got != "New activity" {
		t.Fatalf("fallback to en expected: %q %v", got, err)
	}
	if _, err := Format("en", "nope.key", nil); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key: %v", err)
	}
}

func TestAllMessagesCompile(t *testing.T) {
	c, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range c.Locales() {
		for _, k := range c.Keys() {
			if _, err := c.Format(l, k, map[string]any{"name": "@x", "count": 2}); err != nil {
				t.Errorf("%s/%s: %v", l, k, err)
			}
		}
	}
}
