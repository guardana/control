package otelgenai

import "encoding/json"

// spansPath names the members, outermost first, whose arrays lead to spans.
var spansPath = [...]string{"resourceSpans", "scopeSpans", "spans"}

// countSpans counts the spans a refused line holds, read leniently since the
// strict reader refused it; a line in which none can be counted counts once.
// It walks the bytes of a line json.Valid has passed and decodes nothing, so
// counting allocates nothing per element.
func countSpans(line []byte) uint64 {
	if !json.Valid(line) {
		return 1
	}
	w := walker{b: line}
	w.space()
	return max(w.object(0), 1)
}

// walker reads valid JSON, so it never meets an end or a byte it does not
// expect.
type walker struct {
	b []byte
	i int
}

func (w *walker) space() {
	for w.i < len(w.b) && space(w.b[w.i]) {
		w.i++
	}
}

func space(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// object counts the spans under the value at w.i, an object level members
// down spansPath, and leaves w.i after it. Any other value holds none.
func (w *walker) object(level int) uint64 {
	if w.b[w.i] != '{' {
		w.skip()
		return 0
	}
	w.i++
	var n uint64
	for {
		w.space()
		switch w.b[w.i] {
		case '}':
			w.i++
			return n
		case ',':
			w.i++
			w.space()
		}
		name := w.str()
		w.space()
		w.i++
		w.space()
		if named(name, spansPath[level]) && w.b[w.i] == '[' {
			n += w.array(level)
		} else {
			w.skip()
		}
	}
}

// array counts the spans under the array at w.i: its elements at the last
// member of spansPath, else the spans under each of its elements.
func (w *walker) array(level int) uint64 {
	w.i++
	var n uint64
	for {
		w.space()
		switch w.b[w.i] {
		case ']':
			w.i++
			return n
		case ',':
			w.i++
			w.space()
		}
		if level == len(spansPath)-1 {
			n++
			w.skip()
		} else {
			n += w.object(level + 1)
		}
	}
}

// skip moves w.i past the value it is at.
func (w *walker) skip() {
	switch w.b[w.i] {
	case '"':
		w.str()
	case '{', '[':
		w.container()
	default:
		for w.i < len(w.b) && !boundary(w.b[w.i]) {
			w.i++
		}
	}
}

func boundary(c byte) bool { return c == ',' || c == '}' || c == ']' || space(c) }

// container moves w.i past the object or array it is at.
func (w *walker) container() {
	depth := 0
	for {
		switch w.b[w.i] {
		case '"':
			w.str()
			continue
		case '{', '[':
			depth++
		case '}', ']':
			depth--
		}
		w.i++
		if depth == 0 {
			return
		}
	}
}

// str returns the string at w.i as written, escapes included, and moves w.i
// past its closing quote.
func (w *walker) str() []byte {
	w.i++
	start := w.i
	for w.b[w.i] != '"' {
		if w.b[w.i] == '\\' {
			w.i++
		}
		w.i++
	}
	w.i++
	return w.b[start : w.i-1]
}

// named reports whether raw, a name as written, spells want, which is ASCII.
func named(raw []byte, want string) bool {
	j := 0
	for i := 0; i < len(raw); j++ {
		c := raw[i]
		i++
		if c == '\\' {
			c, i = unescaped(raw, i)
		}
		if j >= len(want) || c != want[j] {
			return false
		}
	}
	return j == len(want)
}

// unescaped reads the escape after a backslash at raw[i]. Any escape that
// does not stand for a printable ASCII byte reads as 0, which no name holds.
func unescaped(raw []byte, i int) (byte, int) {
	switch raw[i] {
	case '"', '\\', '/':
		return raw[i], i + 1
	case 'u':
		var r rune
		for _, h := range raw[i+1 : i+5] {
			r = r<<4 | hexValue(h)
		}
		if r < 0x20 || r > 0x7e {
			r = 0
		}
		return byte(r), i + 5
	}
	return 0, i + 1
}

func hexValue(h byte) rune {
	switch {
	case h >= 'a':
		return rune(h-'a') + 10
	case h >= 'A':
		return rune(h-'A') + 10
	}
	return rune(h - '0')
}
