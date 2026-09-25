package docscheck

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// The targets `make quality` has to run. Adding a target to quality needs no
// change here; dropping one fails this test, so weakening the gate takes an
// edit to the Makefile and one to this list, in the same review. This holds
// the target list only: a recipe edited to ignore its command's exit, or to
// print its pass line unconditionally, passes here, which is what folding each
// pass line into its command (`cmd && echo ...`) in the Makefile is for.
var gateTargets = []string{
	"fmt-check", "vet", "lint", "test", "test-race", "fuzz-smoke", "security",
	"proto-check", "docs-check", "tidy-check", "check-imports",
	"check-imports-probe", "check-brand", "check-sizes", "check-actions",
	"check-shell",
}

func TestQualityRunsTheWholeGate(t *testing.T) {
	prerequisites, err := makePrerequisites(readLines(t, repoFS(t), "Makefile"), "quality")
	if err != nil {
		t.Fatalf("Makefile: %v", err)
	}
	for _, target := range gateTargets {
		if !slices.Contains(prerequisites, target) {
			t.Errorf("make quality no longer runs %s", target)
		}
	}
}

// makePrerequisites returns the prerequisites of target's rule, following
// backslash continuations. No rule, or more than one, is an error.
func makePrerequisites(lines []string, target string) ([]string, error) {
	var prerequisites []string
	rules := 0
	for i := 0; i < len(lines); i++ {
		rest, ok := strings.CutPrefix(lines[i], target+":")
		if !ok {
			continue
		}
		rules++
		for strings.HasSuffix(rest, `\`) && i+1 < len(lines) {
			i++
			rest = strings.TrimSuffix(rest, `\`) + " " + lines[i]
		}
		prerequisites = append(prerequisites, strings.Fields(strings.TrimSuffix(rest, `\`))...)
	}
	if rules != 1 {
		return nil, fmt.Errorf("found %d rule(s) for %s, want 1", rules, target)
	}
	return prerequisites, nil
}

func TestMakePrerequisites(t *testing.T) {
	makefile := strings.Split("quality-quick: a\nquality: a b \\\n         c \\\n         d\n\t@echo quality: green\nother: e\n", "\n")
	got, err := makePrerequisites(makefile, "quality")
	if err != nil || !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("makePrerequisites = %q, %v; want [a b c d]", got, err)
	}
	if _, err := makePrerequisites(makefile, "absent"); err == nil {
		t.Error("a target with no rule read without an error")
	}
	if _, err := makePrerequisites(append(makefile, "quality: f"), "quality"); err == nil {
		t.Error("a target with two rules read without an error")
	}
}
