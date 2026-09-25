package docscheck

import (
	"fmt"
	"slices"
	"strings"

	"github.com/guardana/control/internal/docscheck/frontmatter"
)

const (
	genScriptPrefix = "scripts/gen-"
	genTestPrefix   = "go test "
	markerClose     = " -->"
)

// genRun is one line of the docs-gen recipe: the generator it runs and the
// page it writes with -o.
type genRun struct {
	script, page string
}

// generatedProblems judges what a page says about its generator: a rendered
// page names a script docs-gen runs onto it, or a `go test` pin whose
// package exists; a hand-written page's generated blocks pair, do not nest
// and are each claimed.
func generatedProblems(files []string, runs []genRun, claims []blockClaim, p parsedPage) []string {
	var problems []string
	switch g := p.meta.Generated; {
	case g == "":
	case strings.HasPrefix(g, genScriptPrefix):
		if !slices.Contains(files, g) {
			problems = append(problems, fmt.Sprintf("generated %s names no file", g))
		}
		if !slices.Contains(runs, genRun{g, p.path}) {
			problems = append(problems, fmt.Sprintf("the %s recipe does not run %s -o %s", docsGenTarget, g, p.path))
		}
	case strings.HasPrefix(g, genTestPrefix):
		if problem := testPinProblem(files, g); problem != "" {
			problems = append(problems, problem)
		}
	}
	return append(problems, markerProblems(claims, p)...)
}

// testPinProblem checks a `go test <pkg> -run <Test>` pin names a package
// directory of the walk.
func testPinProblem(files []string, pin string) string {
	fields := strings.Fields(pin)
	if len(fields) != 5 || fields[3] != "-run" {
		return fmt.Sprintf("generated %q is not `go test <pkg> -run <Test>`", pin)
	}
	dir := strings.TrimPrefix(fields[2], "./")
	if !slices.ContainsFunc(files, func(f string) bool { return strings.HasPrefix(f, dir+"/") }) {
		return fmt.Sprintf("generated %q names package %s, which holds no file", pin, dir)
	}
	return ""
}

// markerProblems walks the generated markers outside fenced code: an open
// marker inside an open block nests, a close with no open or an open with
// no close is unpaired, and an open marker no claim covers is unclaimed.
func markerProblems(claims []blockClaim, p parsedPage) []string {
	var problems []string
	fenced, open := false, 0
	for i, line := range strings.Split(string(p.body), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			fenced = !fenced
		case fenced:
		case strings.HasPrefix(trimmed, frontmatter.GeneratedOpen):
			generator := strings.TrimSuffix(strings.TrimPrefix(trimmed, frontmatter.GeneratedOpen), markerClose)
			switch {
			case !strings.HasSuffix(trimmed, markerClose) || generator == "":
				problems = append(problems, fmt.Sprintf("line %d: marker %q does not name a generator", i+1, trimmed))
			case open > 0:
				problems = append(problems, fmt.Sprintf("line %d: generated block %s opens inside another", i+1, generator))
			case !slices.Contains(claims, blockClaim{p.path, generator}):
				problems = append(problems, fmt.Sprintf("line %d: generated block %s is claimed by nobody", i+1, generator))
			}
			open = 1
		case trimmed == frontmatter.GeneratedClose:
			if open == 0 {
				problems = append(problems, fmt.Sprintf("line %d: a generated block closes and none is open", i+1))
			}
			open = 0
		}
	}
	if open > 0 {
		problems = append(problems, "a generated block is never closed")
	}
	return problems
}

// claimProblems is the registry's other direction: every claim names a page
// of the walk whose body opens a generated block naming exactly that
// generator, outside fenced code. A claim on a page with no such marker
// covers nothing, and would let the marker appear later unreviewed.
func claimProblems(claims []blockClaim, pages []parsedPage) []string {
	var problems []string
	for _, claim := range claims {
		i := slices.IndexFunc(pages, func(p parsedPage) bool { return p.path == claim.page })
		switch {
		case i < 0:
			problems = append(problems, fmt.Sprintf("the registry claims a block of %s on %s, which is no page of the walk", claim.generator, claim.page))
		case !slices.Contains(openMarkers(pages[i].body), claim.generator):
			problems = append(problems, fmt.Sprintf("the registry claims a block of %s on %s and the page opens no such block", claim.generator, claim.page))
		}
	}
	return problems
}

// openMarkers lists the generator each open marker outside fenced code
// names, in page order.
func openMarkers(body []byte) []string {
	var generators []string
	fenced := false
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			fenced = !fenced
		case !fenced && strings.HasPrefix(trimmed, frontmatter.GeneratedOpen) && strings.HasSuffix(trimmed, markerClose):
			generators = append(generators, strings.TrimSuffix(strings.TrimPrefix(trimmed, frontmatter.GeneratedOpen), markerClose))
		}
	}
	return generators
}

// docsGenRuns reads the docs-gen recipe of the Makefile: each line runs one
// scripts/gen-<x>.go with -o and a page, and a line of any other shape is
// a problem, because a generator run nobody can pin is a page nobody checks.
func docsGenRuns(makefile []string) ([]genRun, []string) {
	recipe, err := makeRecipe(makefile, docsGenTarget)
	if err != nil {
		return nil, []string{err.Error()}
	}
	var runs []genRun
	var problems []string
	for _, line := range recipe {
		fields := strings.Fields(line)
		i := slices.IndexFunc(fields, func(f string) bool { return strings.HasPrefix(f, genScriptPrefix) && strings.HasSuffix(f, ".go") })
		if i < 0 || i+2 >= len(fields) || fields[i+1] != "-o" {
			problems = append(problems, fmt.Sprintf("%s line %q runs no scripts/gen-<x>.go -o <page>", docsGenTarget, strings.TrimSpace(line)))
			continue
		}
		runs = append(runs, genRun{fields[i], fields[i+2]})
	}
	if len(runs) == 0 {
		problems = append(problems, fmt.Sprintf("%s runs no generator", docsGenTarget))
	}
	return runs, problems
}

// makeRecipe returns the tab-indented lines under target's one rule, with
// backslash continuations joined.
func makeRecipe(lines []string, target string) ([]string, error) {
	var recipe []string
	rules := 0
	for i := 0; i < len(lines); i++ {
		if !strings.HasPrefix(lines[i], target+":") {
			continue
		}
		rules++
		for i+1 < len(lines) && strings.HasPrefix(lines[i+1], "\t") {
			i++
			line := lines[i]
			for strings.HasSuffix(line, `\`) && i+1 < len(lines) {
				i++
				line = strings.TrimSuffix(line, `\`) + " " + strings.TrimSpace(lines[i])
			}
			recipe = append(recipe, line)
		}
	}
	if rules != 1 {
		return nil, fmt.Errorf("found %d rule(s) for %s, want 1", rules, target)
	}
	return recipe, nil
}

// recipeProblems is the other direction: every docs-gen run lands on a page
// that names its script, in its frontmatter or through a claimed block.
func recipeProblems(runs []genRun, pages []parsedPage, claims []blockClaim) []string {
	var problems []string
	for _, run := range runs {
		i := slices.IndexFunc(pages, func(p parsedPage) bool { return p.path == run.page })
		switch {
		case i < 0:
			problems = append(problems, fmt.Sprintf("%s runs %s -o %s, which is no page of the walk", docsGenTarget, run.script, run.page))
		case pages[i].meta.Generated == run.script:
		case slices.Contains(claims, blockClaim{run.page, run.script}) && strings.Contains(string(pages[i].body), frontmatter.GeneratedOpen+run.script+markerClose):
		default:
			problems = append(problems, fmt.Sprintf("%s runs %s -o %s and the page does not name it", docsGenTarget, run.script, run.page))
		}
	}
	return problems
}
