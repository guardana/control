package scenario

import "fmt"

type nodeKind uint8

const (
	kindObject nodeKind = iota + 1
	kindArray
	kindString
	kindNumber
	kindBool
	kindNull
)

// node is one value of the document. start and end bound its bytes in the
// document, which is how arguments and parameters are kept as written.
type node struct {
	kind    nodeKind
	start   int
	end     int
	text    string
	truth   bool
	members []member
	items   []*node
}

type member struct {
	name  string
	value *node
}

// parser reads the document by hand rather than through encoding/json, which
// replaces bytes that are not UTF-8 and lone surrogates without a word and
// keeps the last of two repeated members.
//
// A refusal about what a value holds (null, a repeated member, a string that
// is not UTF-8 or escapes a lone surrogate, text after the document) is noted
// and the parse goes on, so that the kind can be checked before any of them
// is reported. A refusal about the shape of the text ends the parse, since
// nothing after it can be read.
type parser struct {
	raw   []byte
	pos   int
	stack path
	first *Refusal
}

// parse returns the document's root, the first noted refusal and a refusal
// that ended the parse.
func parse(raw []byte) (*node, *Refusal, *Refusal) {
	p := &parser{raw: raw}
	root, fatal := p.value(0)
	if fatal != nil {
		return nil, nil, fatal
	}
	p.ws()
	if p.pos < len(p.raw) {
		p.note(ErrTrailing, p.raw[p.pos:])
	}
	return root, p.first, nil
}

func (p *parser) note(err Error, text []byte) {
	if p.first == nil {
		p.first = refuseQuoting(p.stack, err, "", text)
	}
}

func (p *parser) syntax(what string) *Refusal {
	end := min(p.pos+MaxQuoteBytes, len(p.raw))
	return refuseQuoting(p.stack, ErrSyntax, fmt.Sprintf("%s at byte %d", what, p.pos), p.raw[p.pos:end])
}

func (p *parser) ws() {
	for p.pos < len(p.raw) {
		switch p.raw[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *parser) at(c byte) bool { return p.pos < len(p.raw) && p.raw[p.pos] == c }

// value reads one value; depth is the number of containers around it.
func (p *parser) value(depth int) (*node, *Refusal) {
	p.ws()
	if p.pos >= len(p.raw) {
		return nil, p.syntax("a value is missing")
	}
	switch c := p.raw[p.pos]; {
	case c == '{':
		return p.object(depth + 1)
	case c == '[':
		return p.array(depth + 1)
	case c == '"':
		return p.stringValue()
	case c == 't' || c == 'f' || c == 'n':
		return p.word()
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	return nil, p.syntax("an unexpected byte")
}

func (p *parser) stringValue() (*node, *Refusal) {
	start := p.pos
	s, problem, fatal := p.str()
	if fatal != nil {
		return nil, fatal
	}
	if problem != "" {
		p.note(problem, p.raw[start:p.pos])
	}
	return &node{kind: kindString, start: start, end: p.pos, text: s}, nil
}

func (p *parser) word() (*node, *Refusal) {
	switch p.raw[p.pos] {
	case 't':
		return p.literal("true", &node{kind: kindBool, truth: true})
	case 'f':
		return p.literal("false", &node{kind: kindBool})
	}
	n, fatal := p.literal("null", &node{kind: kindNull})
	if fatal == nil {
		p.note(ErrNull, nil)
	}
	return n, fatal
}

func (p *parser) literal(word string, n *node) (*node, *Refusal) {
	if len(p.raw)-p.pos < len(word) || string(p.raw[p.pos:p.pos+len(word)]) != word {
		return nil, p.syntax("an unknown literal")
	}
	n.start, n.end = p.pos, p.pos+len(word)
	p.pos = n.end
	return n, nil
}

func (p *parser) tooDeep(depth int) *Refusal {
	return refuse(p.stack, ErrTooDeep, fmt.Sprintf("%d containers, the bound is %d", depth, MaxDepth))
}

func (p *parser) object(depth int) (*node, *Refusal) {
	if depth > MaxDepth {
		return nil, p.tooDeep(depth)
	}
	n := &node{kind: kindObject, start: p.pos}
	p.pos++
	p.ws()
	if p.at('}') {
		p.pos++
		n.end = p.pos
		return n, nil
	}
	seen := map[string]bool{}
	for {
		p.ws()
		if !p.at('"') {
			return nil, p.syntax("a member name is missing")
		}
		keyStart := p.pos
		name, problem, fatal := p.str()
		if fatal != nil {
			return nil, fatal
		}
		p.stack = append(p.stack, seg{name: name, index: -1})
		if problem != "" {
			p.note(problem, p.raw[keyStart:p.pos])
		}
		if seen[name] {
			p.note(ErrDuplicate, p.raw[keyStart:p.pos])
		}
		seen[name] = true
		p.ws()
		if !p.at(':') {
			return nil, p.syntax("a colon is missing")
		}
		p.pos++
		v, fatal := p.value(depth)
		if fatal != nil {
			return nil, fatal
		}
		p.stack = p.stack[:len(p.stack)-1]
		n.members = append(n.members, member{name: name, value: v})
		if done, fatal := p.next('}'); fatal != nil || done {
			n.end = p.pos
			return n, fatal
		}
	}
}

func (p *parser) array(depth int) (*node, *Refusal) {
	if depth > MaxDepth {
		return nil, p.tooDeep(depth)
	}
	n := &node{kind: kindArray, start: p.pos}
	p.pos++
	p.ws()
	if p.at(']') {
		p.pos++
		n.end = p.pos
		return n, nil
	}
	for {
		p.stack = append(p.stack, seg{index: len(n.items)})
		v, fatal := p.value(depth)
		if fatal != nil {
			return nil, fatal
		}
		p.stack = p.stack[:len(p.stack)-1]
		n.items = append(n.items, v)
		if done, fatal := p.next(']'); fatal != nil || done {
			n.end = p.pos
			return n, fatal
		}
	}
}

// next reads the comma between two items or the byte that closes the
// container, and reports which.
func (p *parser) next(closer byte) (bool, *Refusal) {
	p.ws()
	switch {
	case p.at(','):
		p.pos++
		return false, nil
	case p.at(closer):
		p.pos++
		return true, nil
	}
	return false, p.syntax("a comma or the end of the container is missing")
}

func (p *parser) number() (*node, *Refusal) {
	start := p.pos
	if p.at('-') {
		p.pos++
	}
	switch {
	case p.at('0'):
		p.pos++
	case p.digits() == 0:
		return nil, p.syntax("a number has no digits")
	}
	if p.at('.') {
		p.pos++
		if p.digits() == 0 {
			return nil, p.syntax("a fraction has no digits")
		}
	}
	if p.at('e') || p.at('E') {
		p.pos++
		if p.at('+') || p.at('-') {
			p.pos++
		}
		if p.digits() == 0 {
			return nil, p.syntax("an exponent has no digits")
		}
	}
	return &node{kind: kindNumber, start: start, end: p.pos, text: string(p.raw[start:p.pos])}, nil
}

func (p *parser) digits() int {
	n := 0
	for p.pos < len(p.raw) && p.raw[p.pos] >= '0' && p.raw[p.pos] <= '9' {
		p.pos++
		n++
	}
	return n
}
