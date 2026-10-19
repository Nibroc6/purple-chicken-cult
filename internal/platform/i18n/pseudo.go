package i18n

import (
	"errors"
	"strings"
	"unicode"
)

// accents is the letter map of web/scripts/pseudo-locale.mjs.
var accents = map[rune]rune{
	'a': 'à', 'b': 'ƀ', 'c': 'ç', 'd': 'ð', 'e': 'ê', 'f': 'ƒ', 'g': 'ĝ', 'h': 'ĥ', 'i': 'î', 'j': 'ĵ', 'k': 'ķ', 'l': 'ļ', 'm': 'ɱ',
	'n': 'ñ', 'o': 'ô', 'p': 'þ', 'q': 'ǫ', 'r': 'ŕ', 's': 'š', 't': 'ŧ', 'u': 'û', 'v': 'ṽ', 'w': 'ŵ', 'x': 'ẋ', 'y': 'ý', 'z': 'ž',
	'A': 'Å', 'B': 'Ɓ', 'C': 'Ç', 'D': 'Ð', 'E': 'Ê', 'F': 'Ƒ', 'G': 'Ĝ', 'H': 'Ĥ', 'I': 'Î', 'J': 'Ĵ', 'K': 'Ķ', 'L': 'Ļ', 'M': 'Ṁ',
	'N': 'Ñ', 'O': 'Ô', 'P': 'Þ', 'Q': 'Ǫ', 'R': 'Ŕ', 'S': 'Š', 'T': 'Ŧ', 'U': 'Û', 'V': 'Ṽ', 'W': 'Ŵ', 'X': 'Ẋ', 'Y': 'Ý', 'Z': 'Ž',
}

var errICU = errors.New("i18n: malformed ICU message")

// PseudoText accents the letters of a literal and pads it by ~40% with "~".
func PseudoText(s string) string {
	var b strings.Builder
	letters := 0
	for _, r := range s {
		if a, ok := accents[r]; ok {
			b.WriteRune(a)
		} else {
			b.WriteRune(r)
		}
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if pad := (letters*4 + 9) / 10; pad > 0 {
		b.WriteString(strings.Repeat("~", pad))
	}
	return b.String()
}

// Pseudo turns an ICU message into its en-XA form: only literal text changes;
// arguments, plural/select branches (whose literals are transformed too), #
// and quoted text are kept; the whole message is bracketed.
func Pseudo(src string) (string, error) {
	p := &icu{s: []rune(src)}
	out, err := p.message(false)
	if err != nil {
		return "", err
	}
	if p.i != len(p.s) {
		return "", errICU
	}
	return "[" + out + "]", nil
}

type icu struct {
	s []rune
	i int
}

// message reads until an unmatched "}" (not consumed) or the end.
func (p *icu) message(plural bool) (string, error) {
	var out, lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out.WriteString(PseudoText(lit.String()))
			lit.Reset()
		}
	}
	for p.i < len(p.s) {
		r := p.s[p.i]
		switch {
		case r == '}':
			flush()
			return out.String(), nil
		case r == '{':
			flush()
			arg, err := p.argument()
			if err != nil {
				return "", err
			}
			out.WriteString(arg)
		case r == '#' && plural:
			flush()
			out.WriteRune(r)
			p.i++
		case r == '\'':
			flush()
			out.WriteString(p.quoted())
		default:
			lit.WriteRune(r)
			p.i++
		}
	}
	flush()
	return out.String(), nil
}

// quoted copies an apostrophe sequence verbatim: ” or a quoted literal.
func (p *icu) quoted() string {
	start := p.i
	p.i++
	if p.i < len(p.s) && p.s[p.i] == '\'' {
		p.i++
		return "''"
	}
	if p.i < len(p.s) && strings.ContainsRune("{}#|", p.s[p.i]) {
		for p.i < len(p.s) {
			if p.s[p.i] == '\'' {
				if p.i+1 < len(p.s) && p.s[p.i+1] == '\'' {
					p.i += 2
					continue
				}
				p.i++
				break
			}
			p.i++
		}
		return string(p.s[start:p.i])
	}
	return "'"
}

// argument reads "{name}", "{name, type[, style]}" or a plural/select with its
// branches, starting at "{".
func (p *icu) argument() (string, error) {
	p.i++ // {
	name := p.until(",}")
	if p.i >= len(p.s) {
		return "", errICU
	}
	if p.s[p.i] == '}' {
		p.i++
		return "{" + name + "}", nil
	}
	p.i++ // ,
	typ := strings.TrimSpace(p.until(",}"))
	if p.i >= len(p.s) {
		return "", errICU
	}
	if p.s[p.i] == '}' {
		p.i++
		return "{" + name + ", " + typ + "}", nil
	}
	p.i++ // ,
	switch typ {
	case "plural", "select", "selectordinal":
	default: // number/date/time styles: copied verbatim
		start, depth := p.i, 0
		for p.i < len(p.s) {
			switch p.s[p.i] {
			case '{':
				depth++
			case '}':
				if depth == 0 {
					style := string(p.s[start:p.i])
					p.i++
					return "{" + name + ", " + typ + "," + style + "}", nil
				}
				depth--
			}
			p.i++
		}
		return "", errICU
	}
	var b strings.Builder
	b.WriteString("{" + name + ", " + typ + ",")
	for {
		p.space()
		if p.i >= len(p.s) {
			return "", errICU
		}
		if p.s[p.i] == '}' {
			p.i++
			b.WriteString("}")
			return b.String(), nil
		}
		sel := p.until("{ \t\n")
		if sel == "" {
			return "", errICU
		}
		b.WriteString(" " + sel)
		if strings.HasPrefix(sel, "offset:") {
			continue
		}
		p.space()
		if p.i >= len(p.s) || p.s[p.i] != '{' {
			return "", errICU
		}
		p.i++
		body, err := p.message(typ != "select")
		if err != nil {
			return "", err
		}
		if p.i >= len(p.s) {
			return "", errICU
		}
		p.i++ // }
		b.WriteString(" {" + body + "}")
	}
}

func (p *icu) until(stop string) string {
	start := p.i
	for p.i < len(p.s) && !strings.ContainsRune(stop, p.s[p.i]) {
		p.i++
	}
	return strings.TrimSpace(string(p.s[start:p.i]))
}

func (p *icu) space() {
	for p.i < len(p.s) && unicode.IsSpace(p.s[p.i]) {
		p.i++
	}
}
