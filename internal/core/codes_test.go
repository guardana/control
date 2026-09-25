package core_test

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/reasons"
	"github.com/guardana/control/pkg/contract"
)

// emission is one code produced by the kernel's own step acting alone. codes
// is the whole list the decision carries: the code under test, and beside it
// at most a matched rule's reason, which is no cause, the availability cause
// a fail-open read needs, or the undetermined veto an unanswered question
// leaves. A code two steps emit has an emission for each.
type emission struct {
	code  string
	codes []string
	req   func() core.Request
	snap  func(t *testing.T) *policy.Snapshot
	opts  core.Options
}

func emissions(t *testing.T) []emission {
	t.Helper()
	refused := func(err error) func() core.Request {
		return func() core.Request {
			req := request(readEnvelope())
			req.Refusal = err
			return req
		}
	}
	allowing := func(t *testing.T) *policy.Snapshot { return snapshot(t, allowReads) }
	none := func(*testing.T) *policy.Snapshot { return nil }
	oneSide := func() core.Request {
		env := writeEnvelope()
		env.Principal.TenantId = ""
		return request(env)
	}
	cross := func() core.Request {
		env := readEnvelope()
		env.Resource.TenantId = "tenant-2"
		return request(env)
	}
	plain := func() core.Request { return request(readEnvelope()) }
	alone := func(code string) []string { return []string{code} }
	answered := func(external core.External) func() core.Request {
		return func() core.Request {
			req := request(readEnvelope())
			req.External = external
			return req
		}
	}
	vetoed := func(t *testing.T) *policy.Snapshot { return snapshot(t, vetoReads) }
	undetermined := func(code string) []string { return []string{codeRuleUndetermined, code} }
	return []emission{
		{codeUnsupportedSchema, alone(codeUnsupportedSchema), refused(contract.ErrUnsupportedSchema), allowing, options()},
		{codeRequiredFieldAbsent, alone(codeRequiredFieldAbsent), refused(contract.ErrMissingField), allowing, options()},
		{codeLimitExceeded, alone(codeLimitExceeded), refused(contract.ErrTooLarge), allowing, options()},
		{codeInvalidFieldValue, alone(codeInvalidFieldValue), refused(contract.ErrInvalidValue), allowing, options()},
		{codeMalformedInput, alone(codeMalformedInput), refused(errors.New("not this contract")), allowing, options()},
		{codeTenantMismatch, []string{codeTenantMismatch, codeRuleAllow}, cross, allowing, options()},
		{codeTenantUndetermined, []string{codeTenantUndetermined, codeRuleAllow}, oneSide,
			func(t *testing.T) *policy.Snapshot { return snapshot(t, allowWrites) }, options()},
		{codePolicyUnavailable, alone(codePolicyUnavailable), plain, none, options()},
		{codePolicyStale, []string{codePolicyStale, codeRuleAllow}, plain,
			func(t *testing.T) *policy.Snapshot { return snapshotAt(t, document(300, allowReads), at(-time.Hour)) }, options()},
		{codeObligationNotUnderstood, []string{codeObligationsAttached, codeObligationNotUnderstood}, plain,
			func(t *testing.T) *policy.Snapshot { return snapshot(t, sandboxedReads) }, options()},
		{codeFailOpenRead, []string{codePolicyUnavailable, codeFailOpenRead}, plain, none, failOpen()},
		{codePDPAllow, []string{codeRuleAllow, codePDPAllow}, answered(core.ExternalAllowed()),
			func(t *testing.T) *policy.Snapshot { return snapshot(t, allowReads, vetoReads) }, options()},
		{codePDPDeny, []string{codeRuleDeny, codePDPDeny}, answered(core.ExternalDenied()), vetoed, options()},
		{codeObligationNotUnderstood, []string{codeRuleDeny, codeObligationNotUnderstood}, answered(core.ExternalDeniedObligations()), vetoed, options()},
		{codePDPTimeout, undetermined(codePDPTimeout), answered(core.ExternalTimeout()), vetoed, failOpen()},
		{codePDPUnavailable, undetermined(codePDPUnavailable), answered(core.ExternalUnavailable()), vetoed, failOpen()},
		{codePDPAnswerRefused, undetermined(codePDPAnswerRefused), answered(core.ExternalAnswerRefused()), vetoed, failOpen()},
	}
}

// TestEmittedCodesCarryTheirStepsVerdict produces each code the kernel spells
// with its step acting alone and holds the verdict reached to the one the
// registry documents. The verdict comes from Decide, the expectation from the
// registry; the kernel never reads the latter.
func TestEmittedCodesCarryTheirStepsVerdict(t *testing.T) {
	for _, e := range emissions(t) {
		t.Run(e.code, func(t *testing.T) {
			out := decide(kernelAt(t, e.opts), e.req(), e.snap(t))
			codes := out.Decision.GetReasonCodes()
			if !slices.Equal(codes, e.codes) {
				t.Fatalf("codes %q, want %q", codes, e.codes)
			}
			registered, ok := reasons.Lookup(e.code)
			if !ok {
				t.Fatalf("%s is emitted and not registered", e.code)
			}
			if got := out.Decision.GetVerdict(); got != registered.Verdict {
				t.Errorf("%s comes with %s, and the registry documents it with %s", e.code, got, registered.Verdict)
			}
		})
	}
}

// TestSourceSpellsNoOtherCode reads the production source for every string
// literal shaped like a reason code: each one has an emission above, and each
// is registered. The delegation codes are the delegation package's to spell.
func TestSourceSpellsNoOtherCode(t *testing.T) {
	var covered []string
	for _, e := range emissions(t) {
		covered = append(covered, e.code)
	}
	slices.Sort(covered)
	covered = slices.Compact(covered)
	found := codeLiterals(t)
	if !slices.Equal(found, covered) {
		t.Errorf("the source spells %q and the emissions cover %q", found, covered)
	}
	for _, code := range found {
		if _, ok := reasons.Lookup(code); !ok {
			t.Errorf("the source spells %s, which the registry does not hold", code)
		}
	}
}

// TestCoreDoesNotReachTheRegistry: no production dependency of this package,
// direct or not, is the registry, which covers rules, match, bundle, policy,
// delegation and approval through the graph. go list reads the files the host
// builds, so every production file's imports are read as well.
func TestCoreDoesNotReachTheRegistry(t *testing.T) {
	self := reflect.TypeFor[core.Kernel]().PkgPath()
	module := strings.TrimSuffix(self, "/internal/core")
	if module == self {
		t.Fatalf("cannot find the module path in %q", self)
	}
	registry := module + "/internal/policy/reasons"
	imported := []string{
		module + "/internal/policy", module + "/internal/policy/match", module + "/internal/policy/rules",
		module + "/internal/core/delegation", module + "/internal/canon", module + "/pkg/contract",
	}
	deps := listDeps(t)
	for _, dep := range append([]string{self}, imported...) {
		if !slices.Contains(deps, dep) {
			t.Fatalf("go list -deps does not list %s, so it did not list this package's dependencies: %q", dep, deps)
		}
	}
	if slices.Contains(deps, registry) {
		t.Error("a production file of this package reaches internal/policy/reasons")
	}
	imports := productionImports(t)
	for _, dep := range []string{module + "/internal/policy", module + "/internal/core/delegation"} {
		if !slices.Contains(imports, dep) {
			t.Fatalf("the production files import %q and not %s, so their source was not read", imports, dep)
		}
	}
	for _, path := range imports {
		if path == registry || strings.HasPrefix(path, registry+"/") {
			t.Errorf("a production file imports %s", path)
		}
	}
}

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
	if len(files) < 2 {
		t.Fatalf("%d production files found; the kernel is more than one", len(files))
	}
	return files
}

func productionImports(t *testing.T) []string {
	t.Helper()
	var imports []string
	for _, name := range productionFiles(t) {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parsing the imports of %s: %v", name, err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s: an import path that does not unquote: %v", name, err)
			}
			imports = append(imports, path)
		}
	}
	slices.Sort(imports)
	return slices.Compact(imports)
}

// codeLiterals returns, sorted and once each, every string literal in the
// production files shaped like a reason code.
func codeLiterals(t *testing.T) []string {
	t.Helper()
	shaped := regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$`)
	var found []string
	for _, name := range productionFiles(t) {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil && shaped.MatchString(s) {
					found = append(found, s)
				}
			}
			return true
		})
	}
	slices.Sort(found)
	return slices.Compact(found)
}

// listDeps runs go list -deps in the package directory. Any failure is fatal:
// a check that could not run must not read like a clean graph.
func listDeps(t *testing.T) []string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "go", "list", "-deps", ".")
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, stderr.String())
	}
	lines := strings.Fields(stdout.String())
	if len(lines) == 0 {
		t.Fatal("go list -deps printed nothing")
	}
	return lines
}
