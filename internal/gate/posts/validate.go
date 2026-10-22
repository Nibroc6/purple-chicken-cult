package posts

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"

	"github.com/Nibroc6/purple-chicken-cult/internal/gate"
	"github.com/Nibroc6/purple-chicken-cult/internal/gate/internal/core"
)

// maxLangLen bounds a BCP-47 tag (RFC 5646 recommends supporting 35).
const maxLangLen = 35

// CodePoints counts Unicode code points (the unit of every length rule).
func CodePoints(s string) int { return utf8.RuneCountInString(s) }

// cleanBody NFC-normalizes body and trims trailing whitespace and leading blank
// lines. Problems: body_empty, body_too_long (> MaxBodyCodePoints code points
// after normalization), bad_request for NUL bytes (Postgres text refuses them).
func cleanBody(body string) (string, error) {
	if strings.IndexByte(body, 0) >= 0 {
		return "", gate.NewProblem(gate.StatusBadRequest, "bad_request", "body")
	}
	b := norm.NFC.String(body)
	b = strings.TrimRightFunc(b, unicode.IsSpace)
	for {
		i := strings.IndexByte(b, '\n')
		if i < 0 || strings.TrimSpace(b[:i]) != "" {
			break
		}
		b = b[i+1:]
	}
	if strings.TrimSpace(b) == "" {
		return "", gate.NewProblem(gate.StatusUnprocessable, "body_empty", "body")
	}
	if CodePoints(b) > MaxBodyCodePoints {
		return "", gate.NewProblem(gate.StatusUnprocessable, "body_too_long", "body")
	}
	return b, nil
}

// cleanLang canonicalizes a BCP-47 tag ("" ⇒ "und"); Problem lang_invalid.
func cleanLang(lang string) (string, error) {
	if lang == "" || lang == "und" {
		return "und", nil
	}
	if len(lang) > maxLangLen {
		return "", gate.NewProblem(gate.StatusUnprocessable, "lang_invalid", "lang")
	}
	t, err := language.Parse(lang)
	if err != nil {
		return "", gate.NewProblem(gate.StatusUnprocessable, "lang_invalid", "lang")
	}
	s := t.String()
	if len(s) > maxLangLen {
		return "", gate.NewProblem(gate.StatusUnprocessable, "lang_invalid", "lang")
	}
	return s, nil
}

// parseAudience parses an API audience (friend_plus: the author's friend+
// friends only, Phase 1).
func parseAudience(a string) (core.Audience, error) {
	switch a {
	case "friend_plus":
		return core.AudFriendPlus, nil
	case "", "connected":
		return core.AudConnected, nil
	case "d1":
		return core.AudD1, nil
	case "d2":
		return core.AudD2, nil
	case "d3":
		return core.AudD3, nil
	}
	return 0, gate.NewProblem(gate.StatusUnprocessable, "audience_invalid", "audience")
}

// parsePolicy parses an API ref policy ("" ⇒ ok=false: use the default).
func parsePolicy(p string) (core.RefPolicy, bool, error) {
	switch p {
	case "":
		return 0, false, nil
	case "anyone":
		return core.PolAnyone, true, nil
	case "two_hops":
		return core.PolTwoHops, true, nil
	case "friends":
		return core.PolFriends, true, nil
	case "nobody":
		return core.PolNobody, true, nil
	}
	return 0, false, gate.NewProblem(gate.StatusUnprocessable, "ref_policy_invalid", "ref_policy")
}

// parseKind parses a parent link kind.
func parseKind(k string) (core.LinkKind, bool) {
	switch k {
	case "", "ref":
		return core.LinkRef, true
	case "cont":
		return core.LinkCont, true
	}
	return 0, false
}
