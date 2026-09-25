package docscheck

import (
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

var (
	atxHeading    = regexp.MustCompile(`^ {0,3}#{1,6}(?:[ \t]+(.*?))?[ \t]*$`)
	closingHashes = regexp.MustCompile(`[ \t]+#+$`)
	inlineLink    = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	// What GitHub drops from a heading to make its anchor: everything but
	// letters, marks, decimal digits, connector punctuation such as "_",
	// spaces and hyphens.
	notInAnchor = regexp.MustCompile(`[^\p{L}\p{M}\p{Nd}\p{Pc} -]`)
)

// headingAnchor spells the anchor GitHub gives a heading: link syntax reduced
// to its text, lower case, the characters above dropped, each space turned
// into a hyphen.
func headingAnchor(text string) string {
	text = closingHashes.ReplaceAllString(text, "")
	text = inlineLink.ReplaceAllString(text, "$1")
	text = notInAnchor.ReplaceAllString(strings.ToLower(text), "")
	return strings.ReplaceAll(text, " ", "-")
}

// anchorsIn returns the anchors a Markdown file offers: one per ATX heading
// outside fenced code, a repeated heading numbered "-1", "-2" and on, as on
// GitHub.
func anchorsIn(lines []string) map[string]bool {
	anchors := make(map[string]bool)
	seen := make(map[string]int)
	fenced := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		m := atxHeading.FindStringSubmatch(line)
		if fenced || m == nil {
			continue
		}
		anchor := headingAnchor(m[1])
		if n := seen[anchor]; n > 0 {
			anchors[anchor+"-"+strconv.Itoa(n)] = true
		} else {
			anchors[anchor] = true
		}
		seen[anchor]++
	}
	return anchors
}

// anchorIndex reads the headings of each link target once.
type anchorIndex struct {
	fsys  fs.FS
	files map[string]map[string]bool
}

func newAnchorIndex(fsys fs.FS) *anchorIndex {
	return &anchorIndex{fsys: fsys, files: make(map[string]map[string]bool)}
}

func (a *anchorIndex) anchors(file string) (map[string]bool, error) {
	if anchors, ok := a.files[file]; ok {
		return anchors, nil
	}
	data, err := fs.ReadFile(a.fsys, file)
	if err != nil {
		return nil, err
	}
	anchors := anchorsIn(strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n"))
	a.files[file] = anchors
	return anchors, nil
}

// linkProblem returns why target, a link written in the Markdown file rel,
// does not resolve, or "" when it does. A remote link is not followed. A
// fragment has to name a heading of the Markdown file it points into; a
// fragment into any other file cannot be checked, and is reported rather than
// skipped.
func linkProblem(a *anchorIndex, rel, target string) string {
	dest, fragment, hasFragment := splitLink(target)
	if uriScheme.MatchString(dest) || (dest == "" && !hasFragment) {
		return ""
	}
	file := rel
	if dest != "" {
		var problem string
		if file, problem = a.resolve(rel, dest, target); problem != "" {
			return problem
		}
	}
	if fragment == "" {
		return "" // no fragment, or "#", the top of the page
	}
	return a.fragmentProblem(file, fragment, target)
}

// splitLink separates a link destination from its optional title and its
// fragment.
func splitLink(target string) (dest, fragment string, hasFragment bool) {
	dest = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(target), "<"), ">")
	if i := strings.IndexAny(dest, " \t"); i >= 0 {
		dest = dest[:i]
	}
	return strings.Cut(dest, "#")
}

func (a *anchorIndex) resolve(rel, dest, target string) (string, string) {
	dir := path.Dir(rel)
	if strings.HasPrefix(dest, "/") {
		dir = "." // a site-root link resolves from the repository root
	}
	file := path.Join(dir, dest)
	if file == ".." || strings.HasPrefix(file, "../") {
		return "", fmt.Sprintf("link %q escapes the repository", target)
	}
	if _, err := fs.Stat(a.fsys, file); err != nil {
		return "", fmt.Sprintf("link %q does not resolve: %v", target, err)
	}
	return file, ""
}

func (a *anchorIndex) fragmentProblem(file, fragment, target string) string {
	name, err := url.PathUnescape(fragment)
	if err != nil {
		return fmt.Sprintf("link %q: the fragment is not valid percent-encoding: %v", target, err)
	}
	if !strings.HasSuffix(file, ".md") {
		return fmt.Sprintf("link %q: only the headings of a Markdown file can be checked, and %s is not one", target, file)
	}
	anchors, err := a.anchors(file)
	switch {
	case err != nil:
		return fmt.Sprintf("link %q: reading %s: %v", target, file, err)
	case !anchors[name]:
		return fmt.Sprintf("link %q: %s has no heading whose anchor is %q", target, file, name)
	}
	return ""
}

// The expected anchors are written out as GitHub renders them, not computed.
func TestHeadingAnchor(t *testing.T) {
	cases := map[string]string{
		"Refusals":                        "refusals",
		"Layers and the dependency rule":  "layers-and-the-dependency-rule",
		"Note 7 — aside — The rule":       "note-7--aside--the-rule",
		"`make quality` runs the gate":    "make-quality-runs-the-gate",
		"What is `INDETERMINATE`?":        "what-is-indeterminate",
		"ADR-0007: Repository layout":     "adr-0007-repository-layout",
		"A [linked](other.md) word":       "a-linked-word",
		"snake_case stays":                "snake_case-stays",
		"Ärger über Öl":                   "ärger-über-öl",
		"1. Scope examined":               "1-scope-examined",
		"Closing hashes ##":               "closing-hashes",
		"C#":                              "c",
		"Tabs, commas; and (parentheses)": "tabs-commas-and-parentheses",
	}
	for heading, want := range cases {
		if got := headingAnchor(heading); got != want {
			t.Errorf("headingAnchor(%q) = %q, want %q", heading, got, want)
		}
	}
}

func TestAnchorsNumberRepeatsAndSkipFences(t *testing.T) {
	lines := strings.Split("# Title\n## Part\ntext\n## Part\n```\n## Fenced\n```\n### Part\n#NoSpace\n####### Seven\n", "\n")
	var got []string
	for anchor := range anchorsIn(lines) {
		got = append(got, anchor)
	}
	slices.Sort(got)
	if want := []string{"part", "part-1", "part-2", "title"}; !slices.Equal(got, want) {
		t.Errorf("anchorsIn = %q, want %q", got, want)
	}
}

func TestLinkProblems(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/a.md": {Data: []byte("# A\n## Section one\n")},
		"docs/b.md": {Data: []byte("## Target\n```\n## Not a heading\n```\n")},
		"docs/c.go": {Data: []byte("package c\n")},
	}
	resolving := []string{
		"b.md", "b.md#target", "./b.md#target", "/docs/b.md#target", "<b.md#target>",
		`b.md#target "a title"`, "#section-one", "#", "c.go",
		"https://example.invalid/page#anything",
	}
	broken := []string{
		"missing.md", "b.md#nope", "b.md#not-a-heading", "b.md#Target", "#missing",
		"c.go#L1", "../../outside.md", "b.md#%zz",
	}
	index := newAnchorIndex(fsys)
	for _, target := range resolving {
		if problem := linkProblem(index, "docs/a.md", target); problem != "" {
			t.Errorf("%q: %s", target, problem)
		}
	}
	for _, target := range broken {
		if linkProblem(index, "docs/a.md", target) == "" {
			t.Errorf("%q resolved; it must not", target)
		}
	}
}
