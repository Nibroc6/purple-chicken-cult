package themes

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
)

// Theme CSS: the advanced mode of a theme (ARCHITECTURE §15.3). It is the
// owner's own raw CSS, applied only to their own view, outside the locked
// routes (sign-in, recovery, settings, moderation, report) so a broken theme
// can always be undone, and dropped under high contrast. The editor warns that
// it can change or hide anything, integrity elements included. image(name)
// names one of the theme's own uploads; the client swaps in its signed URL.
// The CSP still decides what a stylesheet can load (img-src, font-src).
const (
	MaxSheet  = 32 << 10
	MaxImages = 8
)

var (
	// ImageName is the shape of a theme image's name.
	ImageName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	imageRef  = regexp.MustCompile(`image\(\s*([a-z][a-z0-9-]{0,31})\s*\)`)
)

// sheetError is theme_css at a line (field "css.<line>"); line 0 is the sheet.
func sheetError(line int) error {
	return gate.NewProblem(gate.StatusUnprocessable, "theme_css", fmt.Sprintf("css.%d", line))
}

// NormalizeSheet checks src (size, UTF-8, no NUL, every image() name among
// images) and returns it with line endings normalized ("" for a blank sheet).
// Problem: theme_css, field "css.<line>" (css.0 for the sheet as a whole).
func NormalizeSheet(src string, images map[string]bool) (string, error) {
	if len(src) > MaxSheet || !utf8.ValidString(src) {
		return "", sheetError(0)
	}
	src = strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n")
	for i, line := range strings.Split(src, "\n") {
		if strings.ContainsRune(line, 0) {
			return "", sheetError(i + 1)
		}
		for _, m := range imageRef.FindAllStringSubmatch(line, -1) {
			if !images[m[1]] {
				return "", sheetError(i + 1)
			}
		}
	}
	if strings.TrimSpace(src) == "" {
		return "", nil
	}
	return src, nil
}

// SheetImages are the image names src uses.
func SheetImages(src string) map[string]bool {
	out := map[string]bool{}
	for _, m := range imageRef.FindAllStringSubmatch(src, -1) {
		out[m[1]] = true
	}
	return out
}
