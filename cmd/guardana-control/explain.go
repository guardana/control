package main

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/policy/match"
)

// The bounds of an explanation: a document may hold thousands of rules, and a
// rule many constraints, so the output names the first ones and counts the
// rest.
const (
	maxRuleLines = 32
	maxParts     = 8
)

// policyExplain decides one case as `policy test` does and prints how: the
// decision's fields, what the kernel found before the policy, and a line per
// rule. When the case states what it expects, a decision otherwise exits 1
// after the explanation. The output is for a person and carries no version;
// `policy test` is what a script reads.
func policyExplain(path string, stdout, stderr io.Writer) int {
	raw, err := readBounded(path)
	if err != nil {
		return fail(stderr, "policy explain", err)
	}
	c, err := readCaseExpecting(raw, false)
	if err != nil {
		return fail(stderr, "policy explain", err)
	}
	got, err := explainCase(c)
	if err != nil {
		return fail(stderr, "policy explain", err)
	}
	lines := explanationLines(c, got)
	status := exitOK
	if c.hasExpect {
		if got.outcome.equal(c.expect) {
			lines = append(lines, "expect: met")
		} else {
			lines = append(lines, "expect: not met, want "+c.expect.String())
			status = exitFail
		}
	}
	for _, line := range lines {
		if _, err := io.WriteString(stdout, oneLine(line)+"\n"); err != nil {
			return fail(stderr, "policy explain", fmt.Errorf("writing to standard output: %w", err))
		}
	}
	return status
}

// explained is one case decided through Kernel.Explain.
type explained struct {
	outcome  outcome
	decision *controlv1.Decision
	bundleID string
	version  string
	why      core.Explanation
}

func explanationLines(c *testCase, got explained) []string {
	d := got.decision
	lines := []string{
		"verdict: " + verdictName(d.GetVerdict()),
		"kernel_action: " + actionName(got.outcome.action),
		"enforcement_mode: " + strings.TrimPrefix(d.GetEnforcementMode().String(), "ENFORCEMENT_MODE_"),
		"bundle: " + strconv.Quote(got.bundleID) + " " + strconv.Quote(got.version),
		"policy_digest: " + d.GetPolicyBundleDigest(),
		"freshness: " + strings.TrimPrefix(d.GetPolicyFreshness().String(), "POLICY_FRESHNESS_"),
		"reason_codes: " + strings.Join(d.GetReasonCodes(), " "),
		"refused: " + refusedText(got.why.Refusal),
		"clock: " + clockText(got.why.Clock),
		"tenant_unstated: " + orNone(got.why.TenantUnstated),
		"delegation: " + chainText(got.why.Delegation),
		"external: " + c.externalName,
	}
	return append(lines, ruleLines(got.why.Rules, got.why.Refusal != nil)...)
}

func refusedText(r *core.Refusal) string {
	switch {
	case r == nil:
		return "no"
	case r.Field == "":
		return "yes, naming no field"
	}
	return r.Field
}

func clockText(c core.ClockState) string {
	switch c {
	case core.ClockUsable:
		return "usable"
	case core.ClockOutOfRange:
		return "unusable, before 1970 or after 9999"
	case core.ClockBeforeVerified:
		return "unusable, earlier than a time the plane verified"
	}
	return "not checked"
}

func chainText(d core.Delegation) string {
	switch d.State {
	case core.DelegationAbsent:
		return "absent"
	case core.DelegationRefused:
		return "refused " + orNone(d.Code)
	case core.DelegationPassed:
		return "passed"
	}
	return "not checked"
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// ruleLines is the summary and a line per rule, the ones that decided first:
// matched, then undetermined, then ALLOW rules left undetermined, which have
// no effect, then not matched; each group in document order.
func ruleLines(traces []match.RuleTrace, refused bool) []string {
	if traces == nil {
		if refused {
			return []string{"rules: not read, the request was refused first"}
		}
		return []string{"rules: not read, no policy was evaluated"}
	}
	groups := make([][]match.RuleTrace, 4)
	for _, tr := range traces {
		groups[group(tr)] = append(groups[group(tr)], tr)
	}
	lines := []string{fmt.Sprintf("rules: %d read; %d matched, %d undetermined, %d allow undetermined, %d not matched",
		len(traces), len(groups[0]), len(groups[1]), len(groups[2]), len(groups[3]))}
	listed := 0
	for _, g := range groups {
		for _, tr := range g {
			if listed == maxRuleLines {
				return append(lines, "rules_not_listed: "+strconv.Itoa(len(traces)-listed))
			}
			lines = append(lines, ruleLine(tr))
			listed++
		}
	}
	return lines
}

func group(tr match.RuleTrace) int {
	switch {
	case tr.Value == match.ValueYes:
		return 0
	case tr.Value == match.ValueUnknown && tr.Effect != controlv1.Verdict_VERDICT_ALLOW:
		return 1
	case tr.Value == match.ValueUnknown:
		return 2
	}
	return 3
}

func ruleLine(tr match.RuleTrace) string {
	head := "rule " + strconv.Quote(tr.ID) + " " + verdictName(tr.Effect)
	switch group(tr) {
	case 0:
		return head + " matched"
	case 1:
		return head + " undetermined: " + unreadText(tr.Unknown)
	case 2:
		return head + " undetermined, no effect: " + unreadText(tr.Unknown)
	}
	return head + " not matched: " + bounded(tr.No)
}

func unreadText(unknown []match.Unread) string {
	parts := make([]string, len(unknown))
	for i, u := range unknown {
		parts[i] = u.Field + " needs " + strings.Join(u.Needs, " and ")
	}
	return bounded(parts)
}

// bounded joins at most maxParts parts and counts the rest.
func bounded(parts []string) string {
	if len(parts) <= maxParts {
		return strings.Join(parts, "; ")
	}
	return strings.Join(parts[:maxParts], "; ") + "; and " + strconv.Itoa(len(parts)-maxParts) + " more"
}

func verdictName(v controlv1.Verdict) string {
	if name, ok := controlv1.Verdict_name[int32(v)]; ok {
		return strings.TrimPrefix(name, "VERDICT_")
	}
	return fmt.Sprintf("VERDICT(%d)", v)
}

func actionName(a core.EnforcementAction) string {
	for name, known := range actions {
		if known == a {
			return name
		}
	}
	return fmt.Sprintf("EnforcementAction(%d)", a)
}

func (o outcome) equal(other outcome) bool {
	return o.verdict == other.verdict && o.action == other.action && slices.Equal(o.codes, other.codes)
}
