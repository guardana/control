package docscheck

import (
	"fmt"
	"path"
	"slices"
	"strings"
	"testing"
)

const securityTeam = "@guardana/security-maintainers"

// The gate's own Go code, held to the review the scripts beside it are.
var gateTrees = []string{"internal/testreport", "internal/docscheck"}

// Every tree the dependency rule guards, and the gate's Go trees, are owned by
// the security maintainers in CODEOWNERS and need their two approvals in the
// ruleset github-bootstrap.sh creates. A guarded tree added to
// scripts/lib/dependency-rule.sh without both fails here.
func TestCodeownersCoverTheGuardedTrees(t *testing.T) {
	fsys := repoFS(t)
	guarded, err := bareArray(readLines(t, fsys, "scripts/lib/dependency-rule.sh"), "guarded")
	if err != nil {
		t.Fatalf("scripts/lib/dependency-rule.sh: %v", err)
	}
	rules, err := codeownersRules(readLines(t, fsys, ".github/CODEOWNERS"))
	if err != nil {
		t.Fatalf(".github/CODEOWNERS: %v", err)
	}
	twoApproval, err := quotedArray(readLines(t, fsys, "scripts/github-bootstrap.sh"), "TWO_APPROVAL_PATHS")
	if err != nil {
		t.Fatalf("scripts/github-bootstrap.sh: %v", err)
	}
	for _, tree := range slices.Concat(guarded, gateTrees) {
		if owners := ownersOf(rules, tree+"/file.go"); !slices.Contains(owners, securityTeam) {
			t.Errorf(".github/CODEOWNERS gives %s/ to %q, not to %s", tree, owners, securityTeam)
		}
		if !slices.Contains(twoApproval, tree+"/**") {
			t.Errorf("scripts/github-bootstrap.sh: TWO_APPROVAL_PATHS holds no %q", tree+"/**")
		}
	}
}

func TestOwnersOfTakesTheLastMatchingRule(t *testing.T) {
	rules, err := codeownersRules([]string{
		"# a comment",
		"*            @all",
		"/internal/a/ @a",
		"/Makefile    @make",
		"/.lint.*     @lint",
		"/internal/   @internal",
		"/internal/b/ @b # a reason",
	})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"internal/a/x.go":     "@internal",
		"internal/b/c/x.go":   "@b",
		"internal/ab/x.go":    "@internal",
		"Makefile":            "@make",
		"Makefile/x":          "@make",
		"docs/Makefile":       "@all",
		".lint.yml":           "@lint",
		"sub/.lint.yml":       "@all",
		"README.md":           "@all",
		"internal/b":          "@internal",
		"internalx/b/file.go": "@all",
	}
	for file, want := range cases {
		if got := ownersOf(rules, file); !slices.Equal(got, []string{want}) {
			t.Errorf("ownersOf(%q) = %q, want [%s]", file, got, want)
		}
	}
	if got := ownersOf(rules[1:2], "README.md"); got != nil {
		t.Errorf("a file no rule matches is owned by %q", got)
	}
	for _, line := range []string{"internal/a/ @a", "/docs/**/x @a", "/docs/ ", "!/docs/ @a"} {
		if _, err := codeownersRules([]string{line}); err == nil {
			t.Errorf("codeownersRules read %q without an error", line)
		}
	}
}

func TestArrayReadersRefuseWhatTheyCannotRead(t *testing.T) {
	if got, err := quotedArray([]string{"X=(", `  "a/**"`, "  # c", "", `  "b"`, ")"}, "X"); err != nil || !slices.Equal(got, []string{"a/**", "b"}) {
		t.Errorf("quotedArray = %q, %v; want [a/** b]", got, err)
	}
	if got, err := bareArray([]string{"X=(", "  a/b", "  # c", "  c", ")"}, "X"); err != nil || !slices.Equal(got, []string{"a/b", "c"}) {
		t.Errorf("bareArray = %q, %v; want [a/b c]", got, err)
	}
	for name, lines := range map[string][]string{
		"no array":       {"Y=(", `"a"`, ")"},
		"never closed":   {"X=(", `"a"`},
		"empty":          {"X=(", ")"},
		"an unquoted":    {"X=(", "a", ")"},
		"two on a line":  {"X=(", `"a" "b"`, ")"},
		"an expansion":   {"X=(", `"${A}"`, ")"},
		"single-quoted":  {"X=(", `'a'`, ")"},
		"an open string": {"X=(", `"a`, ")"},
	} {
		if got, err := quotedArray(lines, "X"); err == nil {
			t.Errorf("quotedArray read %s as %q without an error", name, got)
		}
	}
	for name, lines := range map[string][]string{
		"empty":   {"X=(", ")"},
		"quoted":  {"X=(", `"a"`, ")"},
		"a space": {"X=(", "a b", ")"},
	} {
		if got, err := bareArray(lines, "X"); err == nil {
			t.Errorf("bareArray read %s as %q without an error", name, got)
		}
	}
}

type codeownersRule struct {
	pattern string
	owners  []string
}

// codeownersRules reads the forms this file uses: "*", an anchored directory
// "/dir/", and an anchored path "/name" that may carry a glob in its last
// element. Any other form is an error rather than a rule matched differently
// from GitHub.
func codeownersRules(lines []string) ([]codeownersRule, error) {
	var rules []codeownersRule
	for _, line := range lines {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		pattern := fields[0]
		if len(fields) < 2 {
			return nil, fmt.Errorf("%q names no owner", pattern)
		}
		if pattern != "*" && (!strings.HasPrefix(pattern, "/") || strings.Contains(path.Dir(strings.TrimSuffix(pattern, "/")), "*")) {
			return nil, fmt.Errorf("%q is not a form this reader matches", pattern)
		}
		rules = append(rules, codeownersRule{pattern: pattern, owners: fields[1:]})
	}
	return rules, nil
}

// ownersOf returns the owners of the last rule that matches file, a
// repository-relative slash path, or nil when none does.
func ownersOf(rules []codeownersRule, file string) []string {
	var owners []string
	for _, r := range rules {
		anchored := strings.TrimPrefix(r.pattern, "/")
		var match bool
		switch {
		case r.pattern == "*":
			match = true
		case strings.HasSuffix(anchored, "/"):
			match = strings.HasPrefix(file, anchored)
		default:
			ok, err := path.Match(anchored, file)
			match = (err == nil && ok) || strings.HasPrefix(file, anchored+"/")
		}
		if match {
			owners = r.owners
		}
	}
	return owners
}

// quotedArray returns the entries of the bash array `name=( ... )`, one
// double-quoted string per line with no expansion in it, blank lines and
// comments skipped. Any other shape is an error, never a shorter list.
func quotedArray(lines []string, name string) ([]string, error) {
	return readArray(lines, name, func(entry string) (string, bool) {
		inner, ok := strings.CutPrefix(entry, `"`)
		inner, closed := strings.CutSuffix(inner, `"`)
		return inner, ok && closed && inner != "" && !strings.ContainsAny(inner, "\"'$`\\ \t")
	})
}

// bareArray returns the entries of the bash array `name=( ... )`, one bare word
// per line with no quote, space, expansion or glob character.
func bareArray(lines []string, name string) ([]string, error) {
	return readArray(lines, name, func(entry string) (string, bool) {
		return entry, !strings.ContainsAny(entry, " \t\"'$`\\(){}*?[]")
	})
}

func readArray(lines []string, name string, word func(string) (string, bool)) ([]string, error) {
	var words []string
	inArray := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case !inArray:
			inArray = trimmed == name+"=("
		case trimmed == ")":
			if len(words) == 0 {
				return nil, fmt.Errorf("array %s is empty", name)
			}
			return words, nil
		case trimmed == "" || strings.HasPrefix(trimmed, "#"):
		default:
			w, ok := word(trimmed)
			if !ok {
				return nil, fmt.Errorf("array %s: %q is not an entry this reader reads", name, trimmed)
			}
			words = append(words, w)
		}
	}
	return nil, fmt.Errorf("no array %s=( ... ) closed by a line holding only \")\"", name)
}
