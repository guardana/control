package docscheck

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

var (
	// A reference definition may be indented up to three spaces; four make
	// it code.
	referenceDefinition = regexp.MustCompile(`^ {0,3}\[([^\]]+)\]:\s*(\S+)`)
	// A full or collapsed reference: [text][label] or [text][].
	referenceUse = regexp.MustCompile(`\[([^\]]*)\]\[([^\]]*)\]`)
	codeSpan     = regexp.MustCompile("``.*?``|`[^`]*`")
	htmlTag      = regexp.MustCompile(`<[A-Za-z][^>]*>`)
	htmlLinkAttr = regexp.MustCompile(`(?i)\s(?:href|src)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'>]+))`)
	listItem     = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s`)
)

func isFence(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "```")
}

// referenceLabel folds a label the way CommonMark matches it: case and runs
// of white space do not count.
func referenceLabel(label string) string {
	return strings.Join(strings.Fields(strings.ToLower(label)), " ")
}

// markdownLinkProblems reports every broken link of one Markdown file: an
// inline link, a reference definition, a reference whose label no definition
// names, and an href or src in raw HTML. Inline links and definitions are
// checked inside fenced code too, because a path shown in an example is
// still a path a reader will follow; a reference and an HTML attribute are
// only links outside code.
func markdownLinkProblems(anchors *anchorIndex, rel string, lines []string) []string {
	var problems []string
	report := func(i int, target string) {
		if problem := linkProblem(anchors, rel, target); problem != "" {
			problems = append(problems, fmt.Sprintf("%s:%d: %s", rel, i+1, problem))
		}
	}
	defined := referenceDefinitions(lines)
	fenced := false
	for i, line := range lines {
		for _, m := range linkTarget.FindAllStringSubmatch(line, -1) {
			report(i, m[1])
		}
		if m := referenceDefinition.FindStringSubmatch(line); m != nil {
			report(i, m[2])
		}
		if isFence(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		prose := codeSpan.ReplaceAllString(line, "")
		for _, label := range undefinedReferences(defined, prose) {
			problems = append(problems, fmt.Sprintf("%s:%d: reference [%s] has no definition", rel, i+1, label))
		}
		for _, tag := range htmlTag.FindAllString(prose, -1) {
			for _, m := range htmlLinkAttr.FindAllStringSubmatch(tag, -1) {
				report(i, m[1]+m[2]+m[3])
			}
		}
	}
	return problems
}

// referenceDefinitions returns the folded label of every definition outside
// fenced code.
func referenceDefinitions(lines []string) map[string]bool {
	defined := map[string]bool{}
	fenced := false
	for _, line := range lines {
		if isFence(line) {
			fenced = !fenced
		} else if m := referenceDefinition.FindStringSubmatch(line); m != nil && !fenced {
			defined[referenceLabel(m[1])] = true
		}
	}
	return defined
}

func undefinedReferences(defined map[string]bool, prose string) []string {
	var labels []string
	for _, m := range referenceUse.FindAllStringSubmatch(prose, -1) {
		label := m[2]
		if label == "" {
			label = m[1]
		}
		if !defined[referenceLabel(label)] {
			labels = append(labels, label)
		}
	}
	return labels
}

// claimBlock is one paragraph, list item, heading or table row, the lines a
// renderer joins into one, with the line number of each.
type claimBlock struct {
	lines []string
	at    []int
}

// claimBlocks splits a file into blocks outside fenced code. A blank line, a
// heading, a list item, a quote and a table row each start a block, so a
// claim wrapped across lines is read whole. An unclosed fence is reported.
func claimBlocks(lines []string) ([]claimBlock, bool) {
	var blocks []claimBlock
	open, fenced := false, false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case isFence(line):
			fenced, open = !fenced, false
			continue
		case fenced || trimmed == "" || tableSeparator.MatchString(line):
			open = false
			continue
		}
		single := strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "|")
		quote := strings.HasPrefix(trimmed, ">")
		if !open || single || listItem.MatchString(line) || quote != strings.HasPrefix(blocks[len(blocks)-1].lines[0], ">") {
			blocks = append(blocks, claimBlock{})
		}
		b := &blocks[len(blocks)-1]
		b.lines = append(b.lines, trimmed)
		b.at = append(b.at, i+1)
		open = !single
	}
	return blocks, !fenced
}

// capabilityClaimProblems reports every capability claim with no status
// label beside it. The claim verb is matched in any case over the block
// joined into one line; the label has to sit on a line the verb touches, or
// on the next line when the verb ends its line.
func capabilityClaimProblems(rel string, lines []string) []string {
	var problems []string
	blocks, closed := claimBlocks(lines)
	for _, b := range blocks {
		joined := strings.Join(b.lines, " ")
		starts := make([]int, len(b.lines))
		for i := 1; i < len(b.lines); i++ {
			starts[i] = starts[i-1] + len(b.lines[i-1]) + 1
		}
		lineOf := func(pos int) int {
			i, found := slices.BinarySearch(starts, pos)
			if !found {
				i--
			}
			return i
		}
		for _, m := range claimPhrase.FindAllStringIndex(joined, -1) {
			first, last := lineOf(m[0]), lineOf(m[1]-2)
			if m[1]-1 >= starts[last]+len(b.lines[last]) && last+1 < len(b.lines) {
				last++
			}
			if !statusLabel.MatchString(strings.Join(b.lines[first:last+1], " ")) {
				problems = append(problems, fmt.Sprintf("%s:%d: capability claim with no status label: %s", rel, b.at[first], b.lines[first]))
			}
		}
	}
	if !closed {
		problems = append(problems, fmt.Sprintf("%s: unclosed code fence; every claim after it would go unchecked", rel))
	}
	return problems
}

func TestMarkdownLinkProblems(t *testing.T) {
	fsys := fstest.MapFS{
		"docs/a.md":     {Data: []byte("# A\n## Section one\n")},
		"site/mark.svg": {Data: []byte("<svg/>")},
	}
	good := "# Title\n\n" +
		"See [the plan][plan], [A][], [again][PLAN] and [the section][sec].\n" +
		"Code is no reference: `m[a][b]`.\n\n" +
		"```go\nx := m[a][b]\n```\n\n" +
		"<img src=\"site/mark.svg\" alt=\"\"> <a href='docs/a.md#section-one'>a</a> <a href=\"https://example.invalid/x\">x</a>\n\n" +
		"[plan]: docs/a.md\n" +
		"   [a]: docs/a.md\n" +
		"[sec]: docs/a.md#section-one\n"
	if problems := markdownLinkProblems(newAnchorIndex(fsys), "README.md", strings.Split(good, "\n")); len(problems) != 0 {
		t.Fatalf("a correct file reported %q", problems)
	}
	cases := map[string]struct{ old, replacement, want string }{
		"an undefined reference":               {"[the plan][plan]", "[the plan][gone-ref]", "README.md:3: reference [gone-ref] has no definition"},
		"an undefined collapsed reference":     {"[A][]", "[B][]", "reference [B] has no definition"},
		"an indented definition to no file":    {"   [a]: docs/a.md", "   [a]: docs/missing.md", `README.md:13: link "docs/missing.md" does not resolve`},
		"an indented definition to no heading": {"   [a]: docs/a.md", "   [a]: docs/a.md#gone", `has no heading whose anchor is "gone"`},
		"a removed definition":                 {"   [a]: docs/a.md\n", "", "reference [A] has no definition"},
		"a definition inside a fence":          {"x := m[a][b]\n```\n", "[only]: docs/a.md\n```\n[x][only]\n", "reference [only] has no definition"},
		"a raw link to no file":                {"href='docs/a.md#section-one'", "href='docs/missing.md'", `README.md:10: link "docs/missing.md" does not resolve`},
		"a raw link to no heading":             {"href='docs/a.md#section-one'", "href='docs/a.md#gone'", `has no heading whose anchor is "gone"`},
		"a raw image of no file":               {`src="site/mark.svg"`, `src="site/none.svg"`, `link "site/none.svg" does not resolve`},
		"an unquoted raw link":                 {"href='docs/a.md#section-one'", "HREF=docs/none.md", `link "docs/none.md" does not resolve`},
	}
	for name, c := range cases {
		text := replaceOnce(t, good, c.old, c.replacement)
		problems := markdownLinkProblems(newAnchorIndex(fsys), "README.md", strings.Split(text, "\n"))
		if !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
}

func TestCapabilityClaimProblems(t *testing.T) {
	for name, c := range map[string]struct {
		text string
		want []int // the lines reported
	}{
		"a labelled claim":                   {"The gateway supports SAML (`planned`).\n", nil},
		"an unlabelled claim":                {"The gateway supports SAML.\n", []int{1}},
		"a capitalised claim":                {"Supports SAML.\n", []int{1}},
		"a claim in capitals":                {"It PROVIDES SAML.\n", []int{1}},
		"a claim wrapped at the verb":        {"Text first.\nThe gateway supports\nSAML.\n", []int{2}},
		"a wrapped claim labelled after":     {"The gateway supports\nSAML (`planned`).\n", nil},
		"a wrapped claim labelled before":    {"The gateway (`planned`) supports\nSAML.\n", nil},
		"a verb pair wrapped":                {"It integrates\nwith SAML.\n", []int{1}},
		"a label two lines away":             {"A label `planned` here.\nMore text.\nIt supports SAML.\n", []int{3}},
		"a label in another paragraph":       {"`planned`\n\nIt supports SAML.\n", []int{3}},
		"a label in another list item":       {"- It supports SAML.\n- `planned` other\n", []int{1}},
		"a label in another table row":       {"| a | It supports SAML |\n| b | `planned` |\n", []int{1}},
		"a labelled table row":               {"| a | It supports SAML | `planned` |\n|---|---|---|\n", nil},
		"a claim inside a fence":             {"```\nIt supports SAML.\n```\n", nil},
		"a claim after a heading":            {"# `planned`\nIt supports SAML.\n", []int{2}},
		"a claim wrapped in a quote":         {"> It supports\n> SAML.\n", []int{1}},
		"a claim after a quote":              {"> `planned`\nIt supports SAML.\n", []int{2}},
		"a wrapped list item":                {"- It supports\n  SAML.\n- `planned`\n", []int{1}},
		"two claims on a labelled line":      {"It supports SAML (`planned`) and provides\nOIDC.\n", nil},
		"a claim with no verb":               {"Supported formats are listed below.\n", nil},
		"a label on the verb's line wrapped": {"Text before. It provides (`experimental`)\nOIDC.\n", nil},
	} {
		problems := capabilityClaimProblems("p.md", strings.Split(c.text, "\n"))
		var got []int
		for _, p := range problems {
			var n int
			if _, err := fmt.Sscanf(p, "p.md:%d:", &n); err != nil {
				t.Fatalf("%s: unreadable problem %q", name, p)
			}
			got = append(got, n)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: reported lines %v, want %v: %q", name, got, c.want, problems)
		}
	}
	if problems := capabilityClaimProblems("p.md", strings.Split("```\nIt supports SAML.\n", "\n")); len(problems) != 1 || !strings.Contains(problems[0], "unclosed code fence") {
		t.Errorf("an unclosed fence reported %q", problems)
	}
}
