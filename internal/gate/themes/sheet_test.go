package themes_test

import (
	"strings"
	"testing"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate/themes"
)

func TestNormalizeSheet(t *testing.T) {
	images := map[string]bool{"heart": true}
	for _, c := range []struct {
		css, want, field string
	}{
		{"", "", ""},
		{"  \n\t", "", ""},
		{".post { display: none }\r\n", ".post { display: none }\n", ""},
		{"button { background: image( heart ) center / contain no-repeat }", "button { background: image( heart ) center / contain no-repeat }", ""},
		{"a{}\n.like { background: image(nope) }", "", "css.2"},
		{"a{}\nb\x00{}", "", "css.2"},
		{"\xff", "", "css.0"},
		{strings.Repeat(" ", themes.MaxSheet+1), "", "css.0"},
	} {
		got, err := themes.NormalizeSheet(c.css, images)
		if c.field != "" {
			if p := problem(t, err); p.Code != "theme_css" || p.Field != c.field {
				t.Errorf("%q: %s %s, want theme_css %s", c.css, p.Code, p.Field, c.field)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q = %q, %v; want %q", c.css, got, err, c.want)
		}
	}
	if used := themes.SheetImages("a { background: image(heart) } b { background: image(logo-2) }"); len(used) != 2 || !used["heart"] || !used["logo-2"] {
		t.Errorf("SheetImages = %v", used)
	}
}
