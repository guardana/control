package supervise

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"

	"github.com/guardana/control/internal/policy/reasons"
	"github.com/guardana/control/internal/policy/strictjson"
)

// Children is how a 0.2 procedure judges the children of the run it
// supervises. The zero value is a 0.1 document's, which states none.
type Children int

// The modes a 0.2 procedure states.
const (
	ChildrenUnstated Children = iota
	// ChildrenInherit judges a root run and every run whose root it is.
	ChildrenInherit
	// ChildrenSeparate judges one run's own calls and lists its children.
	ChildrenSeparate
)

// Binding is a name the steps and allowed tools of a procedure bind, and the
// resource type every call of it carries.
type Binding struct {
	Name, ResourceType string
}

// When is the condition an exception is taken on. The zero value is no
// condition and is never read.
type When int

// The conditions an exception names.
const (
	// WhenApprovalGranted is an approval granted in the call's own trail.
	WhenApprovalGranted When = iota + 1
	// WhenStepFailed is the failure of the step the exception's Subject
	// names.
	WhenStepFailed
	// WhenReason is the reason code in Subject among the call's policy
	// decision's reason codes.
	WhenReason
)

// Exception waives one rule for one step, or, for STEP_OUTSIDE_PROCEDURE,
// for one tool on one upstream, when its condition holds.
type Exception struct {
	ID, Waives     string
	Step           string
	Tool, Upstream string
	When           When
	Subject        string
}

// The bounds of a 0.2 document's lists.
const (
	MaxBindings   = 64
	MaxExceptions = 128
)

// binds reads the binds member of an entry: at most one binding, since a
// call carries one resource and so counts toward one binding.
func (r *reader) binds(o strictjson.Object, where string) string {
	names := r.names(o["binds"], where+" binds")
	switch {
	case len(names) > 1:
		r.fail(errors.New(where + ": a tool in two bindings"))
	case len(names) == 1:
		return names[0]
	}
	return ""
}

// bindings reads each binding's name and resource type, sorted by name.
func (r *reader) bindings(raw json.RawMessage) []Binding {
	if r.err != nil {
		return nil
	}
	o, err := strictjson.ReadObject(raw)
	switch {
	case err != nil:
		r.fail(fmt.Errorf("bindings: %w", err))
		return nil
	case len(o) > MaxBindings:
		r.fail(fmt.Errorf("bindings: %d, bound %d", len(o), MaxBindings))
		return nil
	}
	out := make([]Binding, 0, len(o))
	for _, name := range slices.Sorted(maps.Keys(o)) {
		r.identText(name, "a binding name")
		out = append(out, Binding{Name: name, ResourceType: r.identOf(o[name], "a binding's resource type")})
	}
	return out
}

var exceptionMembers = []string{"id", "waives", "step", "tool", "upstream", "condition"}

func (r *reader) exceptions(raw json.RawMessage) []Exception {
	items := r.array(raw, "exceptions")
	if len(items) > MaxExceptions {
		r.fail(fmt.Errorf("exceptions: %d, bound %d", len(items), MaxExceptions))
		return nil
	}
	out := make([]Exception, 0, len(items))
	for i, item := range items {
		where := "exception " + strconv.Itoa(i+1)
		o := r.objectOf(item, where, exceptionMembers, []string{"id", "waives", "condition"})
		e := Exception{ID: r.ident(o, "id"), Waives: r.ident(o, "waives")}
		_, step := o["step"]
		_, tool := o["tool"]
		_, upstream := o["upstream"]
		switch {
		case step && !tool && !upstream:
			e.Step = r.ident(o, "step")
		case tool && upstream && !step:
			e.Tool, e.Upstream = r.ident(o, "tool"), r.ident(o, "upstream")
		default:
			r.fail(errors.New(where + ": names neither one step nor one tool on one upstream"))
		}
		e.When, e.Subject = r.condition(o["condition"], where+" condition")
		out = append(out, e)
	}
	return out
}

// condition reads an object holding exactly one condition. A reason is one
// the registry holds, so an exception never waits on a code no decision
// carries.
func (r *reader) condition(raw json.RawMessage, where string) (When, string) {
	o := r.objectOf(raw, where, []string{"approval", "step_failed", "reason"}, nil)
	if r.err != nil {
		return 0, ""
	}
	if len(o) != 1 {
		r.fail(errors.New(where + ": not exactly one of approval, step_failed and reason"))
		return 0, ""
	}
	if _, ok := o["approval"]; ok {
		if r.ident(o, "approval") != "granted" {
			r.fail(errors.New(where + ": approval is not granted"))
		}
		return WhenApprovalGranted, ""
	}
	if _, ok := o["step_failed"]; ok {
		return WhenStepFailed, r.ident(o, "step_failed")
	}
	code := r.ident(o, "reason")
	if _, ok := reasons.Lookup(code); !ok {
		r.fail(errors.New(where + ": reason is no registered reason code"))
	}
	return WhenReason, code
}

func (r *reader) children(raw json.RawMessage) Children {
	if r.err != nil {
		return ChildrenUnstated
	}
	switch s, _ := strictjson.String(raw); s {
	case "inherit":
		return ChildrenInherit
	case "separate":
		return ChildrenSeparate
	}
	r.fail(errors.New("children is neither inherit nor separate"))
	return ChildrenUnstated
}
