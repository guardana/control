package match

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy/reasons"
	"github.com/guardana/control/internal/policy/rules"
)

// emittingStep is one step that emits a code with nothing else acting: a
// program of one rule, or no compiled program at all.
type emittingStep struct {
	code string
	rule *rules.Rule // nil: a program nobody compiled
}

func emittingSteps() []emittingStep {
	holds := rules.When{Action: &rules.ActionWhen{Name: []string{"refund"}}}
	fails := rules.When{Action: &rules.ActionWhen{Name: []string{"archive"}}}
	unknown := rules.When{Resource: &rules.ResourceWhen{Environment: []string{"prod"}}}
	return []emittingStep{
		{"RULE_ALLOW", &rules.Rule{ID: "r", Effect: allowEffect, When: holds}},
		{"RULE_DENY", &rules.Rule{ID: "r", Effect: denyEffect, When: holds}},
		{"APPROVAL_REQUIRED", &rules.Rule{ID: "r", Effect: approvalEffect, When: holds}},
		{"OBLIGATIONS_ATTACHED", &rules.Rule{ID: "r", Effect: obligationsEffect, When: holds, Obligations: capped()}},
		{"ENVIRONMENT_BOUNDARY", &rules.Rule{ID: "r", Effect: denyEffect, Reason: "ENVIRONMENT_BOUNDARY", When: holds}},
		{"OUT_OF_SCOPE_ACTION", &rules.Rule{ID: "r", Effect: denyEffect, Reason: "OUT_OF_SCOPE_ACTION", When: holds}},
		{"TOXIC_FLOW_SENSITIVE_TO_EXTERNAL", &rules.Rule{ID: "r", Effect: denyEffect, Reason: "TOXIC_FLOW_SENSITIVE_TO_EXTERNAL", When: holds}},
		{"RULE_UNDETERMINED", &rules.Rule{ID: "r", Effect: approvalEffect, When: unknown}},
		{"NO_MATCHING_RULE", &rules.Rule{ID: "r", Effect: allowEffect, When: fails}},
		{"POLICY_UNAVAILABLE", nil},
	}
}

// TestEmittedCodesCarryTheirStepsVerdict produces each code with the step that
// emits it acting alone, and holds the verdict that step reaches to the one the
// registry documents for the code. The verdict comes from Evaluate and the
// expectation from the registry; neither is read from this package's table.
func TestEmittedCodesCarryTheirStepsVerdict(t *testing.T) {
	env := &controlv1.ActionEnvelope{Action: &controlv1.Action{Name: "refund"}}
	for _, step := range emittingSteps() {
		t.Run(step.code, func(t *testing.T) {
			var p *Program
			if step.rule != nil {
				var err error
				if p, err = Compile(&rules.Document{Rules: []rules.Rule{*step.rule}}); err != nil {
					t.Fatalf("Compile: %v", err)
				}
			}
			got := p.Evaluate(env, Inputs{})
			if !slices.Equal(got.ReasonCodes, []string{step.code}) {
				t.Fatalf("ReasonCodes = %q, want %s alone", got.ReasonCodes, step.code)
			}
			registered, ok := reasons.Lookup(step.code)
			if !ok {
				t.Fatalf("%s is emitted and not registered", step.code)
			}
			if got.Verdict != registered.Verdict {
				t.Errorf("%s comes with %s, and the registry documents it with %s", step.code, got.Verdict, registered.Verdict)
			}
		})
	}
}

// TestSourceEmitsNoOtherCode reads the production source for every string
// literal spelled like a reason code. A code added there without a step above,
// or one the registry does not hold, fails here.
func TestSourceEmitsNoOtherCode(t *testing.T) {
	var covered []string
	for _, step := range emittingSteps() {
		covered = append(covered, step.code)
	}
	slices.Sort(covered)
	found := codeLiterals(t)
	if !slices.Equal(found, covered) {
		t.Errorf("the source spells %q and the steps above cover %q", found, covered)
	}
	for _, code := range found {
		if _, ok := reasons.Lookup(code); !ok {
			t.Errorf("the source spells %s, which the registry does not hold", code)
		}
	}
}

// authorableReasons is the reasons a rule of each effect may name, written
// out: the effect's own default, and three more for DENY. A kernel fact is on
// no list, so a policy cannot dress its own denial as the kernel's.
func authorableReasons() map[controlv1.Verdict][]string {
	return map[controlv1.Verdict][]string{
		allowEffect:       {"RULE_ALLOW"},
		denyEffect:        {"RULE_DENY", "ENVIRONMENT_BOUNDARY", "OUT_OF_SCOPE_ACTION", "TOXIC_FLOW_SENSITIVE_TO_EXTERNAL"},
		approvalEffect:    {"APPROVAL_REQUIRED"},
		obligationsEffect: {"OBLIGATIONS_ATTACHED"},
	}
}

// TestAuthorableReasons crosses every registered code, and a few strings that
// are none, with the four rule effects, against that list.
func TestAuthorableReasons(t *testing.T) {
	authorable := authorableReasons()
	candidates := []string{"rule_deny", " RULE_DENY", "NOT_A_CODE"}
	for _, code := range reasons.All() {
		candidates = append(candidates, code.ID)
	}
	if len(candidates) < 30 {
		t.Fatalf("the registry lists %d codes; this cross is only as good as the list it reads", len(candidates)-3)
	}
	for effect, allowed := range authorable {
		for _, reason := range candidates {
			r := rules.Rule{ID: "r", Effect: effect, Reason: reason, When: rules.When{Action: &rules.ActionWhen{Name: []string{"refund"}}}}
			if effect == obligationsEffect {
				r.Obligations = capped()
			}
			p, err := Compile(&rules.Document{Rules: []rules.Rule{r}})
			switch {
			case slices.Contains(allowed, reason) && err != nil:
				t.Errorf("%s with reason %s is refused: %v", effect, reason, err)
			case slices.Contains(allowed, reason):
				got := p.Evaluate(&controlv1.ActionEnvelope{Action: &controlv1.Action{Name: "refund"}}, Inputs{})
				if !slices.Equal(got.ReasonCodes, []string{reason}) {
					t.Errorf("%s with reason %s gives codes %q", effect, reason, got.ReasonCodes)
				}
			case !errors.Is(err, errReason):
				t.Errorf("%s with reason %q: Compile = %v, want errReason", effect, reason, err)
			}
		}
	}
}

// TestMatchDoesNotReachTheRegistry is the import-graph half of "a verdict never
// comes from a reason code": no production dependency of this package, direct
// or not, is the registry. The test files may import it; go list -deps without
// -test does not read them.
//
// go list reads only the files the host's platform builds, so the source of
// every production file is read as well, whatever its build constraints say:
// a file built only on another platform may not import the registry either.
func TestMatchDoesNotReachTheRegistry(t *testing.T) {
	const registry = "github.com/guardana/control/internal/policy/reasons"
	imported := []string{"github.com/guardana/control/internal/policy/rules", "github.com/guardana/control/pkg/contract"}
	deps := goList(t, "-deps", ".")
	// The listing is real only if it holds what this package does import.
	for _, dep := range append([]string{"github.com/guardana/control/internal/policy/match"}, imported...) {
		if !slices.Contains(deps, dep) {
			t.Fatalf("go list -deps does not list %s, so it did not list this package's dependencies: %q", dep, deps)
		}
	}
	if slices.Contains(deps, registry) {
		t.Error("a production file of this package reaches internal/policy/reasons")
	}
	imports := productionImports(t)
	for _, dep := range imported {
		if len(imports[dep]) == 0 {
			t.Fatalf("the production files import %q and not %s, so their source was not read", slices.Sorted(maps.Keys(imports)), dep)
		}
	}
	for path, files := range imports {
		if path == registry || strings.HasPrefix(path, registry+"/") {
			t.Errorf("%q import %s", files, path)
		}
	}
}

// productionFiles lists the package's production Go files, whatever their
// build constraints say.
func productionFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() && strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			files = append(files, name)
		}
	}
	if len(files) == 0 {
		t.Fatal("no production file found; this test reads its subject from the source")
	}
	return files
}

// productionImports maps each import path of the production files to the
// files that import it, reading each file's import block alone.
func productionImports(t *testing.T) map[string][]string {
	t.Helper()
	imports := make(map[string][]string)
	for _, name := range productionFiles(t) {
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", name), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing the imports of %s: %v", name, err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: an import path that does not unquote: %v", name, err)
			}
			imports[path] = append(imports[path], name)
		}
	}
	return imports
}

// codeLiterals returns, sorted and once each, every string literal in the
// package's production files that is spelled like a reason code.
func codeLiterals(t *testing.T) []string {
	t.Helper()
	var found []string
	for _, name := range productionFiles(t) {
		found = append(found, codeLiteralsIn(t, name)...)
	}
	slices.Sort(found)
	return slices.Compact(found)
}

// codeLiteralsIn returns the string literals of one file spelled like a reason
// code, repeats included.
func codeLiteralsIn(t *testing.T, name string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", name), nil, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", name, err)
	}
	spelled := regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil {
			t.Fatalf("%s: a string literal that does not unquote: %v", name, err)
		}
		if spelled.MatchString(s) {
			found = append(found, s)
		}
		return true
	})
	return found
}

// goList runs go list in the package directory. Any failure is fatal: a check
// that could not run must not read like a clean graph.
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	// The program is the fixed string "go" and every argument is a literal in
	// this file; gosec cannot see that through the variadic call.
	//nolint:gosec // G204: no external input reaches this argument list.
	cmd := exec.CommandContext(t.Context(), "go", append([]string{"list"}, args...)...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.Fields(stdout.String())
}
