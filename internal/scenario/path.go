package scenario

import (
	"strconv"
	"strings"
)

// seg is one step of a member path: an array index, or, when index is
// negative, an object member.
type seg struct {
	name  string
	index int
}

// path names a value in the document, as steps[2].call.answer.codes[0].
type path []seg

// key returns p extended by a member. The copy keeps a sibling's path from
// being written through a shared backing array.
func (p path) key(name string) path {
	return append(p[:len(p):len(p)], seg{name: name, index: -1})
}

// index returns p extended by an array index.
func (p path) index(i int) path {
	return append(p[:len(p):len(p)], seg{index: i})
}

// String renders the path. A member name that is not a plain word, which
// the author of arguments or parameters may write, is quoted and bounded
// like any other quote.
func (p path) String() string {
	var b strings.Builder
	for _, s := range p {
		switch {
		case s.index >= 0:
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(s.index))
			b.WriteByte(']')
		case plainWord(s.name):
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			b.WriteString(s.name)
		default:
			q, cut := quote([]byte(s.name))
			b.WriteString(`["`)
			b.WriteString(q)
			if cut {
				b.WriteString("...")
			}
			b.WriteString(`"]`)
		}
	}
	return b.String()
}

func plainWord(s string) bool {
	if s == "" || len(s) > MaxQuoteBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}
