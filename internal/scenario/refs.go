package scenario

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/guardana/control/internal/canon"
)

// refOpen starts every reference, and nothing else may use it in arguments.
const refOpen = "${"

// refString is one string value of the arguments that holds a reference:
// where its token stands in the arguments and what it decodes to, cut into
// literal text and references.
type refString struct {
	start  int
	end    int
	pieces []piece
}

// piece is literal text, or, when step is not negative, the output of that
// step.
type piece struct {
	text string
	step int
}

// Arguments returns the call's arguments with each reference replaced by the
// output of the step it names, as outputs holds it by step index. The string
// holding a reference is decoded, every reference in it replaced in one pass
// left to right, and the result encoded as canon encodes a string; the text
// put in is never read for references again. Every other byte is the
// document's. An output outputs does not hold is an error, as is one canon
// cannot encode. What an output may be is the caller's to enforce.
func (c *Call) Arguments(outputs map[int]string) ([]byte, error) {
	var b bytes.Buffer
	last := 0
	for _, rs := range c.refs {
		b.Write(c.Args[last:rs.start])
		var s strings.Builder
		for _, p := range rs.pieces {
			if p.step < 0 {
				s.WriteString(p.text)
				continue
			}
			out, ok := outputs[p.step]
			if !ok {
				return nil, fmt.Errorf("%w: the output of step %d was not handed in", ErrOutput, p.step)
			}
			s.WriteString(out)
		}
		enc, err := canon.Canonicalize(s.String())
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrOutput, err)
		}
		b.Write(enc)
		last = rs.end
	}
	b.Write(c.Args[last:])
	return b.Bytes(), nil
}

// references finds every string value of args that holds a reference and
// refuses a "${" anywhere else or not starting a well-formed reference to an
// earlier call answered with a result.
func (st *steps) references(args *node, at path) ([]refString, *Refusal) {
	w := &refWalk{st: st, base: args.start, stack: at}
	if r := w.walk(args); r != nil {
		return nil, r
	}
	return w.found, nil
}

// refWalk keeps the member path as a stack, pushed and popped, so a walk over
// wide arguments does not copy the path at every member.
type refWalk struct {
	st    *steps
	base  int
	stack path
	found []refString
}

func (w *refWalk) walk(n *node) *Refusal {
	switch n.kind {
	case kindObject:
		for _, m := range n.members {
			w.stack = append(w.stack, seg{name: m.name, index: -1})
			if strings.Contains(m.name, refOpen) {
				return refuseQuoting(w.stack, ErrReference, "a reference stands only in a string value", []byte(m.name))
			}
			if r := w.walk(m.value); r != nil {
				return r
			}
			w.stack = w.stack[:len(w.stack)-1]
		}
	case kindArray:
		for i, item := range n.items {
			w.stack = append(w.stack, seg{index: i})
			if r := w.walk(item); r != nil {
				return r
			}
			w.stack = w.stack[:len(w.stack)-1]
		}
	case kindString:
		return w.str(n)
	}
	return nil
}

func (w *refWalk) str(n *node) *Refusal {
	if !strings.Contains(n.text, refOpen) {
		return nil
	}
	var pieces []piece
	for rest := n.text; rest != ""; {
		k := strings.Index(rest, refOpen)
		if k < 0 {
			pieces = append(pieces, piece{text: rest, step: -1})
			break
		}
		if k > 0 {
			pieces = append(pieces, piece{text: rest[:k], step: -1})
		}
		step, length, ok := reference(rest[k:])
		if !ok {
			return refuseQuoting(w.stack, ErrReference, "want ${step[n].output}", []byte(rest[k:]))
		}
		if r := w.source(step); r != nil {
			return r
		}
		pieces = append(pieces, piece{step: step})
		rest = rest[k+length:]
	}
	w.found = append(w.found, refString{start: n.start - w.base, end: n.end - w.base, pieces: pieces})
	return nil
}

// source refuses a reference to anything but an earlier call answered with a
// result: no other answer has an output to put in.
func (w *refWalk) source(i int) *Refusal {
	if i >= len(w.st.read) {
		return refuse(w.stack, ErrStep, fmt.Sprintf("step %d is not an earlier step", i))
	}
	if c := w.st.read[i].Call; c == nil || c.Answer.Kind != AnswerResult {
		return refuse(w.stack, ErrStep, fmt.Sprintf("step %d is not a call answered with a result", i))
	}
	return nil
}

// reference reads "${step[n].output}" at the start of s and returns n and
// the reference's length.
func reference(s string) (int, int, bool) {
	const head, tail = "${step[", "].output}"
	rest, ok := strings.CutPrefix(s, head)
	if !ok {
		return 0, 0, false
	}
	end := strings.Index(rest, tail)
	if end < 0 {
		return 0, 0, false
	}
	n, ok := decimal(rest[:end])
	return n, len(head) + end + len(tail), ok
}
