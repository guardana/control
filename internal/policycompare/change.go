package policycompare

import (
	"slices"

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
)

// Change is how a case's decision moved from the current policy to the
// candidate.
type Change string

const (
	// Unchanged is the same verdict, action, codes and obligations.
	Unchanged Change = "unchanged"
	// Stricter is an action further from running the call: the after
	// blocks, holds or adds obligations where the before ran it, holds
	// where it ran it with obligations, or blocks where it held it.
	Stricter Change = "stricter"
	// Looser is the reverse of Stricter.
	Looser Change = "looser"
	// Other is the same action with a different verdict, codes or
	// obligations.
	Other Change = "other"
	// Unknown is a case unknown on either side, never one of the four.
	Unknown Change = "unknown"
)

// Changes is every change, in the order the report counts them.
func Changes() []Change { return []Change{Unchanged, Stricter, Looser, Other, Unknown} }

// side is one policy's decision of one case.
type side struct {
	evaluated   bool
	err         string
	verdict     controlv1.Verdict
	action      core.EnforcementAction
	codes       []string
	obligations []*controlv1.Obligation
	// missing is every input an undetermined rule lacked, once each, in the
	// order the rules read them.
	missing  []string
	external bool
	stale    bool
}

// strictness orders the four actions by how far each is from running the
// call: run, run under obligations, hold for an approval, block.
var strictness = map[core.EnforcementAction]int{
	core.Execute:                0,
	core.ExecuteWithObligations: 1,
	core.AwaitApproval:          2,
	core.Block:                  3,
}

// classify compares two decisions of one case. A side not evaluated, or an
// action this order does not name, is unknown rather than guessed.
func classify(before, after side) Change {
	b, knownB := strictness[before.action]
	a, knownA := strictness[after.action]
	switch {
	case !before.evaluated || !after.evaluated || !knownB || !knownA:
		return Unknown
	case a > b:
		return Stricter
	case a < b:
		return Looser
	case before.verdict == after.verdict && slices.Equal(before.codes, after.codes) &&
		slices.EqualFunc(before.obligations, after.obligations, func(x, y *controlv1.Obligation) bool { return proto.Equal(x, y) }):
		return Unchanged
	}
	return Other
}
