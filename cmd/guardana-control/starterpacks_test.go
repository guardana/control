package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/rules"
)

const starterPacksDir = "../../examples/starter-packs"

// starterPacks are named rather than listed from the directory, so a pack
// that goes missing fails its tests instead of dropping out of them.
var starterPacks = []string{"read-only", "approval-for-writes"}

// starterCaseFiles returns a pack's case files and fails the test when there
// are none.
func starterCaseFiles(t *testing.T, pack string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(starterPacksDir, pack, "cases", "*.json"))
	if err != nil {
		t.Fatalf("%s: %v", pack, err)
	}
	if len(files) == 0 {
		t.Fatalf("%s: no case file", pack)
	}
	return files
}

// starterPolicy parses a pack's policy.json and returns the document and its
// canonical bytes.
func starterPolicy(t *testing.T, pack string) (*rules.Document, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(starterPacksDir, pack, "policy.json")) //nolint:gosec // G304: a pack this repository ships
	if err != nil {
		t.Fatalf("%s: %v", pack, err)
	}
	doc, canonical, err := rules.Parse(raw)
	if err != nil {
		t.Fatalf("%s: policy.json: %v", pack, err)
	}
	return doc, canonical
}

func starterCase(t *testing.T, path string) *testCase {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a case of a pack this repository ships
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	c, err := readCase(raw)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return c
}

func TestStarterPacksPass(t *testing.T) {
	for _, pack := range starterPacks {
		t.Run(pack, func(t *testing.T) {
			files := starterCaseFiles(t, pack)
			code, stdout, stderr := invoke(t, "policy", "test", filepath.Join(starterPacksDir, pack, "cases"))
			if code != 0 || stderr != "" {
				t.Errorf("exit %d, stderr %q, stdout:\n%s", code, stderr, stdout)
			}
			lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
			if len(lines) != len(files) {
				t.Errorf("%d lines for %d case files:\n%s", len(lines), len(files), stdout)
			}
			for _, line := range lines {
				if !strings.HasPrefix(line, "ok   ") {
					t.Errorf("line %q is not a pass", line)
				}
			}
		})
	}
}

// A case is evidence about the pack only while it decides against the pack's
// own document; a copy that drifted would pass while the pack changed.
func TestStarterPackCasesCarryThePacksDocument(t *testing.T) {
	for _, pack := range starterPacks {
		t.Run(pack, func(t *testing.T) {
			_, want := starterPolicy(t, pack)
			for _, path := range starterCaseFiles(t, pack) {
				_, got, err := rules.Parse(starterCase(t, path).document)
				if err != nil {
					t.Errorf("%s: document: %v", filepath.Base(path), err)
					continue
				}
				if !bytes.Equal(got, want) {
					t.Errorf("%s: the document is not the pack's policy.json", filepath.Base(path))
				}
			}
		})
	}
}

func TestStarterPacksCoverAllowDenyAndUnknown(t *testing.T) {
	for _, pack := range starterPacks {
		t.Run(pack, func(t *testing.T) {
			seen := map[controlv1.Verdict]bool{}
			for _, path := range starterCaseFiles(t, pack) {
				seen[starterCase(t, path).expect.verdict] = true
			}
			if !seen[controlv1.Verdict_VERDICT_ALLOW] && !seen[controlv1.Verdict_VERDICT_REQUIRE_APPROVAL] {
				t.Error("no case expects ALLOW or REQUIRE_APPROVAL")
			}
			if !seen[controlv1.Verdict_VERDICT_DENY] {
				t.Error("no case expects DENY")
			}
			if !seen[controlv1.Verdict_VERDICT_INDETERMINATE] {
				t.Error("no case expects INDETERMINATE")
			}
		})
	}
}

func TestStarterPackPoliciesLint(t *testing.T) {
	for _, pack := range starterPacks {
		t.Run(pack, func(t *testing.T) {
			code, stdout, stderr := invoke(t, "policy", "lint", filepath.Join(starterPacksDir, pack, "policy.json"))
			if code != 0 || stdout != "" || stderr != "" {
				t.Errorf("exit %d, stdout %q, stderr %q; want 0 and nothing", code, stdout, stderr)
			}
		})
	}
}

// The approval rule lists the classes rather than "everything but READ",
// which the format cannot say, so a class added to the contract would pass
// through the pack as NO_MATCHING_RULE until the list names it.
func TestStarterPackForApprovalAsksForEveryMaterialClass(t *testing.T) {
	doc, _ := starterPolicy(t, "approval-for-writes")
	i := slices.IndexFunc(doc.Rules, func(r rules.Rule) bool { return r.ID == "writes-need-approval" })
	if i < 0 {
		t.Fatal("no rule writes-need-approval")
	}
	rule := doc.Rules[i]
	if rule.Effect != controlv1.Verdict_VERDICT_REQUIRE_APPROVAL {
		t.Errorf("effect %v, want REQUIRE_APPROVAL", rule.Effect)
	}
	if rule.When.Action == nil || !reflect.DeepEqual(rule.When, rules.When{Action: &rules.ActionWhen{Effect: rule.When.Action.Effect}}) {
		t.Fatalf("the rule constrains more than action.effect: %+v", rule.When)
	}
	var want []controlv1.EffectClass
	values := controlv1.EffectClass(0).Descriptor().Values()
	for j := range values.Len() {
		class := controlv1.EffectClass(values.Get(j).Number())
		if class != controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED && class != controlv1.EffectClass_EFFECT_CLASS_READ {
			want = append(want, class)
		}
	}
	if len(want) == 0 {
		t.Fatal("the descriptor lists no material class; nothing was compared")
	}
	got := slices.Clone(rule.When.Action.Effect)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("action.effect = %v, want %v", got, want)
	}
}
