package frontmatter

import (
	"strings"
	"unicode"
)

// Generated blocks inside a hand-written page are delimited by these lines,
// and a budget does not count them, as it does not count a fenced block.
const (
	GeneratedOpen  = "<!-- generated: "
	GeneratedClose = "<!-- /generated -->"
)

// Words counts the words of a body: the white-space separated tokens that
// hold a letter or a digit, outside fenced code blocks and generated blocks.
// A token of punctuation alone, such as a table's pipe, is not a word.
func Words(body []byte) int {
	n := 0
	fenced, generated := false, false
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			fenced = !fenced
			continue
		case fenced:
			continue
		case strings.HasPrefix(trimmed, GeneratedOpen):
			generated = true
			continue
		case trimmed == GeneratedClose:
			generated = false
			continue
		case generated:
			continue
		}
		for _, token := range strings.Fields(line) {
			if strings.ContainsFunc(token, isWordRune) {
				n++
			}
		}
	}
	return n
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
