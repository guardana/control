package scenario

import (
	"strings"
	"unicode/utf8"
)

// str reads a string token and returns it decoded, with the first problem
// its content has. A byte that is not UTF-8 and a lone surrogate decode to
// U+FFFD only so the parse can go on; the problem refuses the document.
func (p *parser) str() (string, Error, *Refusal) {
	p.pos++
	var b []byte
	var problem Error
	for {
		if p.pos >= len(p.raw) {
			return "", "", p.syntax("a string is not closed")
		}
		if p.raw[p.pos] == '"' {
			p.pos++
			return string(b), problem, nil
		}
		var bad Error
		var fatal *Refusal
		if b, bad, fatal = p.char(b); fatal != nil {
			return "", "", fatal
		}
		if problem == "" {
			problem = bad
		}
	}
}

// char appends the character at pos, escaped or not, to b and says what is
// wrong with it.
func (p *parser) char(b []byte) ([]byte, Error, *Refusal) {
	switch c := p.raw[p.pos]; {
	case c == '\\':
		r, bad, fatal := p.escape()
		if fatal != nil {
			return nil, "", fatal
		}
		b = utf8.AppendRune(b, r)
		if bad {
			return b, ErrSurrogate, nil
		}
		return b, "", nil
	case c < 0x20:
		return nil, "", p.syntax("a control character inside a string")
	case c < utf8.RuneSelf:
		p.pos++
		return append(b, c), "", nil
	}
	r, width := utf8.DecodeRune(p.raw[p.pos:])
	b = append(b, p.raw[p.pos:p.pos+width]...)
	p.pos += width
	if r == utf8.RuneError && width <= 1 {
		return b, ErrNotUTF8, nil
	}
	return b, "", nil
}

// escape reads one escape and returns the rune it stands for, and whether it
// is a lone surrogate.
func (p *parser) escape() (rune, bool, *Refusal) {
	if p.pos+1 >= len(p.raw) {
		return 0, false, p.syntax("an escape is not finished")
	}
	c := p.raw[p.pos+1]
	if i := strings.IndexByte(`"\/bfnrt`, c); i >= 0 {
		p.pos += 2
		return rune("\"\\/\b\f\n\r\t"[i]), false, nil
	}
	if c != 'u' {
		return 0, false, p.syntax("an unknown escape")
	}
	return p.unicodeEscape()
}

// unicodeEscape reads \uXXXX. A high surrogate takes the low one that must
// follow it; either half alone is reported.
func (p *parser) unicodeEscape() (rune, bool, *Refusal) {
	r, ok := p.hex4(p.pos + 2)
	if !ok {
		return 0, false, p.syntax("a \\u escape without four hex digits")
	}
	p.pos += 6
	switch {
	case r >= 0xDC00 && r <= 0xDFFF:
		return utf8.RuneError, true, nil
	case r >= 0xD800 && r <= 0xDBFF:
		if p.at('\\') && p.pos+1 < len(p.raw) && p.raw[p.pos+1] == 'u' {
			if low, ok := p.hex4(p.pos + 2); ok && low >= 0xDC00 && low <= 0xDFFF {
				p.pos += 6
				return 0x10000 + (r-0xD800)<<10 + (low - 0xDC00), false, nil
			}
		}
		return utf8.RuneError, true, nil
	}
	return r, false, nil
}

func (p *parser) hex4(at int) (rune, bool) {
	if at+4 > len(p.raw) {
		return 0, false
	}
	var r rune
	for _, c := range p.raw[at : at+4] {
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			return 0, false
		}
		r = r<<4 | rune(v)
	}
	return r, true
}
