package secretscan

import "math"

// noMatch is above every pattern id.
const noMatch = math.MaxInt

// decoding is how a text's bytes are read before they are matched.
type decoding int

const (
	asWritten decoding = iota
	percentEscapes
	percentEscapesAndPlus
)

type edge struct {
	b  byte
	to int
}

// ref is what a pattern id reports.
type ref struct {
	key      string
	spelling string
}

// matcher is an Aho-Corasick automaton over every spelling of every secret.
// A pattern's id is its position in configuration order, entry first, so
// the lowest id found in a text is the verdict the per-pattern search gave,
// and one pass costs the same whatever the number of patterns.
type matcher struct {
	root  [256]int
	edges [][]edge
	fail  []int
	// best is the lowest id of a pattern that ends at the state, its own or
	// one reached through its failure links.
	best []int
	refs []ref
}

func newMatcher(entries []entry) *matcher {
	m := &matcher{edges: [][]edge{nil}, best: []int{noMatch}}
	for _, e := range entries {
		for _, p := range e.patterns {
			m.insert(p.text, len(m.refs))
			m.refs = append(m.refs, ref{key: e.key, spelling: p.spelling})
		}
	}
	m.link()
	return m
}

func (m *matcher) insert(text string, id int) {
	s := 0
	for i := 0; i < len(text); i++ {
		s = m.child(s, text[i])
	}
	m.best[s] = min(m.best[s], id)
}

func (m *matcher) child(s int, c byte) int {
	for _, e := range m.edges[s] {
		if e.b == c {
			return e.to
		}
	}
	to := len(m.edges)
	m.edges = append(m.edges, nil)
	m.best = append(m.best, noMatch)
	m.edges[s] = append(m.edges[s], edge{b: c, to: to})
	return to
}

// link sets the failure links breadth first, so a state's link and its
// link's best are final before the state's own children are reached.
func (m *matcher) link() {
	m.fail = make([]int, len(m.edges))
	queue := make([]int, 0, len(m.edges))
	for _, e := range m.edges[0] {
		m.root[e.b] = e.to
		queue = append(queue, e.to)
	}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		for _, e := range m.edges[s] {
			f := m.next(m.fail[s], e.b)
			m.fail[e.to] = f
			m.best[e.to] = min(m.best[e.to], m.best[f])
			queue = append(queue, e.to)
		}
	}
}

func (m *matcher) next(s int, c byte) int {
	for s != 0 {
		for _, e := range m.edges[s] {
			if e.b == c {
				return e.to
			}
		}
		s = m.fail[s]
	}
	return m.root[c]
}

// first is the lowest pattern id in the text read with the given decoding,
// or noMatch. A malformed escape is read as written.
func (m *matcher) first(text string, d decoding) int {
	best, s := noMatch, 0
	for i := 0; i < len(text); {
		c, n := text[i], 1
		if d != asWritten {
			c, n = decodeByte(text, i, d)
		}
		i += n
		if s == 0 {
			s = m.root[c]
		} else {
			s = m.next(s, c)
		}
		if b := m.best[s]; b < best {
			if best = b; best == 0 {
				break
			}
		}
	}
	return best
}

// decodeByte is the byte at text[i] read with the given decoding and how
// many bytes of the text it takes.
func decodeByte(text string, i int, d decoding) (byte, int) {
	c := text[i]
	switch {
	case d == asWritten:
	case c == '%' && i+2 < len(text) && isHex(text[i+1]) && isHex(text[i+2]):
		return unhex(text[i+1])<<4 | unhex(text[i+2]), 3
	case c == '+' && d == percentEscapesAndPlus:
		return ' ', 1
	}
	return c, 1
}

// hasEscape reports whether percent-decoding changes the text.
func hasEscape(text string) bool {
	for i := 0; i+2 < len(text); i++ {
		if text[i] == '%' && isHex(text[i+1]) && isHex(text[i+2]) {
			return true
		}
	}
	return false
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c <= 'F':
		return c - 'A' + 10
	default:
		return c - 'a' + 10
	}
}
