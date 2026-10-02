package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// explainOf runs `policy explain` on one case written into a directory of the
// test's own, and returns the exit status and stdout's lines.
func explainOf(t *testing.T, content string) (int, []string) {
	t.Helper()
	path := filepath.Join(caseDir(t, map[string]string{"case.json": content}), "case.json")
	code, stdout, stderr := invoke(t, "policy", "explain", path)
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	if !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("stdout does not end in a newline: %q", stdout)
	}
	return code, strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
}

// withoutExpect is a case with its expect member taken out.
func withoutExpect(p parts) string {
	return strings.Replace(p.json(), `,"expect":`+p.expect, "", 1)
}

var digestLine = regexp.MustCompile(`^policy_digest: sha256:[0-9a-f]{64}$`)

// TestPolicyExplainPrintsTheDecisionAndEveryRule: every line, written out,
// on a document whose four rules land in the four groups, in that order and
// not in the document's.
func TestPolicyExplainPrintsTheDecisionAndEveryRule(t *testing.T) {
	p := base()
	p.document = `{"apiVersion":"agent-policy/v1alpha1","bundle":{"id":"orders","version":"7","serial":1,"maxStaleSeconds":300},"rules":[` +
		`{"id":"deny-refunds","effect":"DENY","when":{"action":{"name":["refund"]}}},` +
		`{"id":"allow-staff","effect":"ALLOW","when":{"principal":{"attributes":{"role":["staff"]}}}},` +
		`{"id":"no-gold","effect":"DENY","when":{"action":{"effect":["READ"]},"resource":{"labels":{"tier":["gold"]},"environment":["prod"]},"agent":{"framework":["x"]}}},` +
		allowReads + `]}`
	code, lines := explainOf(t, withoutExpect(p))
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	want := []string{
		"verdict: INDETERMINATE",
		"kernel_action: Block",
		"enforcement_mode: ENFORCE",
		`bundle: "orders" "7"`,
		"",
		"freshness: FRESH",
		"reason_codes: RULE_ALLOW RULE_UNDETERMINED",
		"refused: no",
		"tenant_unstated: none",
		"delegation: absent",
		"external: not asked",
		"rules: 4 read; 1 matched, 1 undetermined, 1 allow undetermined, 1 not matched",
		`rule "allow-reads" ALLOW matched`,
		`rule "no-gold" DENY undetermined: when.agent.framework needs agent.framework; when.resource.labels["tier"] needs resource.labels["tier"]`,
		`rule "allow-staff" ALLOW undetermined, no effect: when.principal.attributes["role"] needs principal.attributes["role"]`,
		`rule "deny-refunds" DENY not matched: when.action.name`,
	}
	if len(lines) != len(want) || !digestLine.MatchString(lines[4]) {
		t.Fatalf("lines:\n%s", strings.Join(lines, "\n"))
	}
	lines[4] = ""
	if !slices.Equal(lines, want) {
		t.Errorf("lines:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

// TestPolicyExplainHoldsACaseToItsExpectation: met is exit 0, not met is the
// whole explanation and exit 1, as `policy test` counts it.
func TestPolicyExplainHoldsACaseToItsExpectation(t *testing.T) {
	code, lines := explainOf(t, base().json())
	if code != 0 || lines[len(lines)-1] != "expect: met" {
		t.Errorf("exit %d, last line %q; want 0 and expect: met", code, lines[len(lines)-1])
	}
	wrong := base()
	wrong.expect = `{"verdict":"DENY","action":"Block","reason_codes":["RULE_DENY"]}`
	code, lines = explainOf(t, wrong.json())
	if code != 1 || lines[len(lines)-1] != "expect: not met, want DENY Block [RULE_DENY]" || lines[0] != "verdict: ALLOW" {
		t.Errorf("exit %d, lines %q; want 1, the explanation and the unmet expectation", code, lines)
	}
	stale := base()
	stale.loadedAt = `"2026-09-11T11:00:00Z"`
	stale.expect = `{"verdict":"INDETERMINATE","action":"Block","reason_codes":["POLICY_STALE","RULE_ALLOW"]}`
	code, lines = explainOf(t, stale.json())
	if code != 0 || lines[len(lines)-1] != "expect: met" {
		t.Errorf("two codes in order: exit %d, last line %q; want 0 and expect: met", code, lines[len(lines)-1])
	}
	code, lines = explainOf(t, withoutExpect(base()))
	if code != 0 || slices.ContainsFunc(lines, func(l string) bool { return strings.HasPrefix(l, "expect:") }) {
		t.Errorf("a case without expect: exit %d, lines %q", code, lines)
	}
}

// TestPolicyTestStillRequiresAnExpectation: explain reads expect as optional,
// and policy test still refuses a case without it.
func TestPolicyTestStillRequiresAnExpectation(t *testing.T) {
	code, lines := policyTestOf(t, map[string]string{"case.json": withoutExpect(base())})
	if code != 1 || len(lines) != 1 || lines[0] != "FAIL case.json: expect: missing" {
		t.Errorf("exit %d, lines %q", code, lines)
	}
}

// TestPolicyExplainBoundsItsOutput: forty rules print thirty-two rule lines
// and count the rest; a rule with ten unknown constraints names eight.
func TestPolicyExplainBoundsItsOutput(t *testing.T) {
	var rules []string
	for i := range 40 {
		rules = append(rules, fmt.Sprintf(`{"id":"deny-%02d","effect":"DENY","when":{"action":{"name":["refund"]}}}`, i))
	}
	labels := make([]string, 10)
	for i := range labels {
		labels[i] = fmt.Sprintf(`"k%d":["v"]`, i)
	}
	rules = append(rules, `{"id":"labelled","effect":"DENY","when":{"resource":{"labels":{`+strings.Join(labels, ",")+`}}}}`)
	p := base()
	p.document = `{"apiVersion":"agent-policy/v1alpha1","bundle":{"id":"orders","version":"1","serial":1,"maxStaleSeconds":300},"rules":[` +
		strings.Join(rules, ",") + `]}`
	_, lines := explainOf(t, withoutExpect(p))
	ruleLines := slices.DeleteFunc(slices.Clone(lines), func(l string) bool { return !strings.HasPrefix(l, "rule ") })
	if len(ruleLines) != 32 {
		t.Errorf("%d rule lines, want 32", len(ruleLines))
	}
	if last := lines[len(lines)-1]; last != "rules_not_listed: 9" {
		t.Errorf("last line %q, want rules_not_listed: 9", last)
	}
	first := ruleLines[0]
	if !strings.HasPrefix(first, `rule "labelled" DENY undetermined: `) || !strings.HasSuffix(first, "; and 2 more") ||
		strings.Count(first, " needs ") != 8 {
		t.Errorf("the undetermined rule's line: %q", first)
	}
}

// TestPolicyExplainNamesARefusalWithoutRepeatingIt: an envelope missing its
// effect class names the field; one with an unknown member holding text and
// a key marker names none and repeats neither.
func TestPolicyExplainNamesARefusalWithoutRepeatingIt(t *testing.T) {
	unclassified := base()
	unclassified.envelope = strings.Replace(readEnvelope, `"effect":"EFFECT_CLASS_READ",`, "", 1)
	leaky := base()
	leaky.envelope = strings.Replace(readEnvelope, `"requestId":"req-1",`, `"requestId":"req-1","sentinel-7f3a":"-----BEGIN PRIVATE KEY-----",`, 1)
	for name, tc := range map[string]struct {
		p       parts
		refused string
	}{
		"no effect class":   {unclassified, "refused: action.effect"},
		"an unknown member": {leaky, "refused: yes, naming no field"},
	} {
		t.Run(name, func(t *testing.T) {
			_, lines := explainOf(t, withoutExpect(tc.p))
			if !slices.Contains(lines, tc.refused) || !slices.Contains(lines, "rules: not read, the request was refused first") {
				t.Errorf("lines %q, want %q and no rule read", lines, tc.refused)
			}
			if out := strings.Join(lines, "\n"); strings.Contains(out, "sentinel-7f3a") || strings.Contains(out, "PRIVATE KEY") {
				t.Errorf("the explanation repeats the envelope: %s", out)
			}
		})
	}
}

// TestPolicyExplainAgreesWithPolicyTest: for every case this repository
// ships, explain's decision lines are what policy test prints for the case.
func TestPolicyExplainAgreesWithPolicyTest(t *testing.T) {
	dirs := []string{casesDir, filepath.Join("..", "..", "testdata", "policy", "fixtures"),
		filepath.Join("..", "..", "examples", "starter-packs", "read-only", "cases"),
		filepath.Join("..", "..", "examples", "starter-packs", "approval-for-writes", "cases")}
	seen := 0
	for _, dir := range dirs {
		names, err := caseFiles(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, name := range names {
			path := filepath.Join(dir, name)
			line, ok := runCase(path)
			if !ok {
				t.Errorf("%s: policy test fails it: %s", path, line)
				continue
			}
			code, stdout, stderr := invoke(t, "policy", "explain", path)
			if code != 0 || stderr != "" {
				t.Errorf("%s: exit %d, stderr %q", path, code, stderr)
				continue
			}
			got := decisionOf(strings.Split(stdout, "\n"))
			if got != line {
				t.Errorf("%s: explain decided %q, policy test %q", path, got, line)
			}
			seen++
		}
	}
	if seen < 40 {
		t.Errorf("compared %d cases; the shipped directories hold more", seen)
	}
}

// decisionOf spells explain's verdict, action and codes as policy test's line.
func decisionOf(lines []string) string {
	value := func(name string) string {
		for _, l := range lines {
			if v, ok := strings.CutPrefix(l, name+": "); ok {
				return v
			}
		}
		return "<no " + name + ">"
	}
	return value("verdict") + " " + value("kernel_action") + " [" + value("reason_codes") + "]"
}

// TestPolicyExplainRefusesWhatItCannotRead: a file that is not a case, and a
// path that is not there, are one stderr line and exit 1.
func TestPolicyExplainRefusesWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"document":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"not a case": bad, "no file": filepath.Join(dir, "none.json")} {
		code, stdout, stderr := invoke(t, "policy", "explain", path)
		if code != 1 || stdout != "" || !strings.HasPrefix(oneStderrLine(t, stderr), brand.CLI+": policy explain: ") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, stdout, stderr)
		}
	}
}
