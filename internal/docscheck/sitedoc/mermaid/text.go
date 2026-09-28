package mermaid

import (
	"fmt"
	"strings"
	"unicode"
)

// isSpace is the whitespace the grammar strips and splits on: Unicode
// White_Space and the four information separators U+001C to U+001F.
func isSpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
}

func strip(s string) string {
	return strings.TrimFunc(s, isSpace)
}

// isWord is a character an entity code's name is made of: a letter, a number
// or an underscore. A combining mark is not one.
func isWord(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// isUpper counts the circled and Roman-numeral capitals as upper case, which
// unicode.IsUpper does not.
func isUpper(r rune) bool {
	return unicode.Is(unicode.Lu, r) || unicode.Is(unicode.Other_Uppercase, r)
}

// splitLines breaks a source at every Unicode line boundary, a CR LF pair
// counting once, so the line numbers in an error match what an editor shows.
func splitLines(source string) []string {
	var lines []string
	start := 0
	for i, r := range source {
		if i < start {
			continue
		}
		width := 0
		switch r {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			width = len(string(r))
		case '\r':
			width = 1
			if strings.HasPrefix(source[i+1:], "\n") {
				width = 2
			}
		default:
			continue
		}
		lines = append(lines, source[start:i])
		start = i + width
	}
	if start < len(source) {
		lines = append(lines, source[start:])
	}
	return lines
}

// quote writes s the way an error message shows a piece of the source: in
// single quotes, or in double quotes when s holds a single quote and no
// double one, with control and unprintable characters escaped.
func quote(s string) string {
	mark := '\''
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		mark = '"'
	}
	var b strings.Builder
	b.WriteRune(mark)
	for _, r := range s {
		switch r {
		case mark, '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteString(escapeRune(r))
		}
	}
	b.WriteRune(mark)
	return b.String()
}

func escapeRune(r rune) string {
	switch {
	case r < ' ' || r == 0x7f:
		return fmt.Sprintf(`\x%02x`, r)
	case r < 0x7f || unicode.IsPrint(r):
		return string(r)
	case r <= 0xff:
		return fmt.Sprintf(`\x%02x`, r)
	case r <= 0xffff:
		return fmt.Sprintf(`\u%04x`, r)
	}
	return fmt.Sprintf(`\U%08x`, r)
}

var escaper = strings.NewReplacer(
	"&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;",
)

func escape(s string) string {
	return escaper.Replace(s)
}

// drawable refuses text holding a character XML 1.0 cannot carry, which
// would make the whole page unreadable to a strict parser.
func drawable(text string) error {
	for _, r := range text {
		if r == '\t' || r == '\n' || r == '\r' ||
			(r >= 0x20 && r <= 0xd7ff) || (r >= 0xe000 && r <= 0xfffd) || r >= 0x10000 {
			continue
		}
		return fmt.Errorf("%s holds a character XML cannot carry", quote(text))
	}
	return nil
}

// hasEntity reports whether text holds an entity code: `#`, one or more
// word characters, then `;`.
func hasEntity(text string) bool {
	for i := strings.IndexByte(text, '#'); i >= 0; {
		rest := text[i+1:]
		name := strings.IndexFunc(rest, func(r rune) bool { return !isWord(r) })
		if name > 0 && rest[name] == ';' {
			return true
		}
		next := strings.IndexByte(rest, '#')
		if next < 0 {
			return false
		}
		i += 1 + next
	}
	return false
}

// breakLines splits a label at each `<br>`, in any case and with optional
// space and slash before the `>`.
func breakLines(text string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(text); i++ {
		if end := breakEnd(text, i); end > 0 {
			lines = append(lines, strip(text[start:i]))
			start = end
			i = end - 1
		}
	}
	return append(lines, strip(text[start:]))
}

func breakEnd(text string, i int) int {
	if len(text)-i < 4 || text[i] != '<' || text[i+1]|0x20 != 'b' || text[i+2]|0x20 != 'r' {
		return 0
	}
	rest := strings.TrimLeftFunc(text[i+3:], isSpace)
	rest = strings.TrimPrefix(rest, "/")
	if !strings.HasPrefix(rest, ">") {
		return 0
	}
	return len(text) - len(rest) + 1
}
