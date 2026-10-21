package supervise

import (
	"slices"
	"strings"
)

// RuleIDs is every rule a 0.1 procedure configures, in the order a report
// lists them. The slice is a copy.
func RuleIDs() []string { return slices.Clone(ruleIDs) }

// ApplyAlone applies rule id to in with no other rule applied before it, and
// returns the anchor of each finding it would raise, its parts joined by a
// slash.
func ApplyAlone(in Input, id string) ([]string, error) {
	if err := checkInput(in); err != nil {
		return nil, err
	}
	rd := newRead(judged(in), in.Run.Tenant)
	if err := rd.takeEvents(in.Exports); err != nil {
		return nil, err
	}
	rd.takeObservations(in.Sources)
	var out []string
	for _, d := range newEvaluation(in, rd).apply(id) {
		out = append(out, strings.Join(d.anchor, "/"))
	}
	return out, nil
}
