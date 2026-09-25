package scenario

import (
	"bytes"
	"fmt"
	"strings"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/reasons"
	"github.com/guardana/control/internal/policy/rules"
	"github.com/guardana/control/pkg/contract"
)

// approvalPending is the one code a pending answer carries.
const approvalPending = "APPROVAL_PENDING"

// steps is what the steps read so far let a later step refer to.
type steps struct {
	raw      []byte
	read     []Step
	unpaused map[int]bool
	answered map[int]bool
}

func readSteps(n *node, at path, raw []byte) ([]Step, *Refusal) {
	if n.kind != kindArray {
		return nil, wrongType(at, "an array")
	}
	if len(n.items) == 0 || len(n.items) > MaxSteps {
		return nil, refuse(at, ErrBound, fmt.Sprintf("%d steps, want 1 to %d", len(n.items), MaxSteps))
	}
	st := &steps{raw: raw, read: make([]Step, 0, len(n.items)), unpaused: map[int]bool{}, answered: map[int]bool{}}
	expects := false
	for i, item := range n.items {
		step, r := st.step(item, at.index(i))
		if r != nil {
			return nil, r
		}
		st.read = append(st.read, step)
		expects = expects || (step.Call != nil && len(step.Call.Trail.Kinds) > 0)
	}
	if !expects {
		return nil, refuse(at, ErrNoEvidence, "a scenario whose trails may all be empty examines nothing")
	}
	return st.read, nil
}

var stepKinds = []string{"call", "approve", "reject", "pause", "unpause"}

func (st *steps) step(n *node, at path) (Step, *Refusal) {
	if n.kind != kindObject {
		return Step{}, wrongType(at, "an object")
	}
	if _, r := closed(n, at, nil, stepKinds...); r != nil {
		return Step{}, r
	}
	if len(n.members) != 1 {
		return Step{}, refuse(at, ErrStep, "a step holds exactly one of "+strings.Join(stepKinds, ", "))
	}
	m := n.members[0]
	at = at.key(m.name)
	var s Step
	var r *Refusal
	switch m.name {
	case "call":
		s.Call, r = st.call(m.value, at)
	case "approve":
		s.Approve, r = st.approval(m.value, at)
	case "reject":
		s.Reject, r = st.approval(m.value, at)
	case "pause":
		s.Pause, r = readPause(m.value, at)
	default:
		s.Unpause, r = st.unpause(m.value, at)
	}
	return s, r
}

func (st *steps) call(n *node, at path) (*Call, *Refusal) {
	f, r := closed(n, at, []string{"tool", "args", "answer", "decided", "trail"})
	if r != nil {
		return nil, r
	}
	c := &Call{}
	if c.Tool, r = text(f["tool"], at.key("tool")); r != nil {
		return nil, r
	}
	if c.Tool == "" || len(c.Tool) > contract.MaxStringBytes {
		return nil, refuse(at.key("tool"), ErrBound, fmt.Sprintf("%d bytes, want 1 to %d", len(c.Tool), contract.MaxStringBytes))
	}
	args := f["args"]
	if args.kind != kindObject {
		return nil, wrongType(at.key("args"), "an object")
	}
	c.Args = bytes.Clone(st.raw[args.start:args.end])
	if c.refs, r = st.references(args, at.key("args")); r != nil {
		return nil, r
	}
	if c.Answer, r = readAnswer(f["answer"], at.key("answer")); r != nil {
		return nil, r
	}
	if c.Decided, r = st.decided(f["decided"], at.key("decided")); r != nil {
		return nil, r
	}
	if c.Trail, r = st.trail(f["trail"], at.key("trail")); r != nil {
		return nil, r
	}
	return c, nil
}

func registered(id string) bool { _, ok := reasons.Lookup(id); return ok }

func readAnswer(n *node, at path) (Answer, *Refusal) {
	f, r := closed(n, at, []string{"kind", "codes"})
	if r != nil {
		return Answer{}, r
	}
	var a Answer
	s, r := text(f["kind"], at.key("kind"))
	if r != nil {
		return Answer{}, r
	}
	a.Kind = AnswerKind(s)
	switch a.Kind {
	case AnswerResult, AnswerError, AnswerBlocked, AnswerPending:
	default:
		return Answer{}, refuseQuoting(at.key("kind"), ErrValue, "want result, error, blocked or pending", []byte(s))
	}
	if a.Codes, r = names(f["codes"], at.key("codes"), registered, "not a registered reason code"); r != nil {
		return Answer{}, r
	}
	switch {
	case (a.Kind == AnswerResult || a.Kind == AnswerError) && len(a.Codes) != 0:
		return Answer{}, refuse(at.key("codes"), ErrValue, fmt.Sprintf("a %s answer carries no code", a.Kind))
	case a.Kind == AnswerPending && (len(a.Codes) != 1 || a.Codes[0] != approvalPending):
		return Answer{}, refuse(at.key("codes"), ErrValue, "a pending answer carries "+approvalPending+" alone")
	}
	return a, nil
}

func (st *steps) decided(n *node, at path) (Decided, *Refusal) {
	switch n.kind {
	case kindString:
		if n.text != "none" {
			return Decided{}, refuseQuoting(at, ErrValue, `want "none" or an object`, []byte(n.text))
		}
		return Decided{None: true}, nil
	case kindObject:
	default:
		return Decided{}, wrongType(at, `"none" or an object`)
	}
	f, r := closed(n, at, []string{"verdict", "codes", "obligations"})
	if r != nil {
		return Decided{}, r
	}
	var d Decided
	s, r := text(f["verdict"], at.key("verdict"))
	if r != nil {
		return Decided{}, r
	}
	v, ok := controlv1.Verdict_value["VERDICT_"+s]
	if !ok || v == int32(controlv1.Verdict_VERDICT_UNSPECIFIED) {
		return Decided{}, refuseQuoting(at.key("verdict"), ErrValue, "not a verdict", []byte(s))
	}
	d.Verdict = controlv1.Verdict(v)
	if d.Codes, r = names(f["codes"], at.key("codes"), registered, "not a registered reason code"); r != nil {
		return Decided{}, r
	}
	items, r := list(f["obligations"], at.key("obligations"))
	if r != nil {
		return Decided{}, r
	}
	d.Obligations = make([]Obligation, len(items))
	for i, item := range items {
		if d.Obligations[i], r = st.obligation(item, at.key("obligations").index(i)); r != nil {
			return Decided{}, r
		}
	}
	return d, nil
}

func (st *steps) obligation(n *node, at path) (Obligation, *Refusal) {
	f, r := closed(n, at, []string{"type", "params", "advisory"})
	if r != nil {
		return Obligation{}, r
	}
	var o Obligation
	if o.Type, r = text(f["type"], at.key("type")); r != nil {
		return Obligation{}, r
	}
	if !rules.KnownObligation(o.Type) {
		return Obligation{}, refuseQuoting(at.key("type"), ErrValue, "not an obligation of the catalogue", []byte(o.Type))
	}
	params := f["params"]
	if params.kind != kindObject {
		return Obligation{}, wrongType(at.key("params"), "an object")
	}
	o.Params = bytes.Clone(st.raw[params.start:params.end])
	advisory := f["advisory"]
	if advisory.kind != kindBool {
		return Obligation{}, wrongType(at.key("advisory"), "true or false")
	}
	o.Advisory = advisory.truth
	return o, nil
}

func (st *steps) trail(n *node, at path) (Trail, *Refusal) {
	f, r := closed(n, at, []string{"request", "kinds"})
	if r != nil {
		return Trail{}, r
	}
	t := Trail{Request: NewRequest}
	s, r := text(f["request"], at.key("request"))
	if r != nil {
		return Trail{}, r
	}
	if s != "new" {
		held, ok := stepName(s)
		if !ok {
			return Trail{}, refuseQuoting(at.key("request"), ErrValue, `want "new" or "step[n]"`, []byte(s))
		}
		if r := st.pendingCall(held, at.key("request")); r != nil {
			return Trail{}, r
		}
		t.Request = held
	}
	kinds, r := names(f["kinds"], at.key("kinds"), eventKind, "not an event kind")
	if r != nil {
		return Trail{}, r
	}
	t.Kinds = make([]controlv1.EventKind, len(kinds))
	for i, k := range kinds {
		t.Kinds[i] = controlv1.EventKind(controlv1.EventKind_value["EVENT_KIND_"+k])
	}
	return t, nil
}

func eventKind(name string) bool {
	v, ok := controlv1.EventKind_value["EVENT_KIND_"+name]
	return ok && v != int32(controlv1.EventKind_EVENT_KIND_UNSPECIFIED)
}

// pendingCall refuses a step index that does not name an earlier call step
// answered with a pending approval.
func (st *steps) pendingCall(i int, at path) *Refusal {
	if i >= len(st.read) {
		return refuse(at, ErrStep, fmt.Sprintf("step %d is not an earlier step", i))
	}
	if c := st.read[i].Call; c == nil || c.Answer.Kind != AnswerPending {
		return refuse(at, ErrStep, fmt.Sprintf("step %d is not a call answered with a pending approval", i))
	}
	return nil
}
