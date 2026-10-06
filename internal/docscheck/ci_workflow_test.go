package docscheck

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

const (
	ciWorkflowPath = ".github/workflows/ci.yml"
	ciGateJob      = "quality"
	ciGateCommand  = "make quality"
)

// The keys the gate job and its gate step may carry. Anything else can skip
// the job, mask its result or change what the command runs: if, needs,
// continue-on-error, strategy, env, defaults, container, shell,
// working-directory and keys no one has thought of yet.
var (
	ciJobKeys  = []string{"name", "runs-on", "timeout-minutes", "steps"}
	ciStepKeys = []string{"name", "run"}
)

// yamlNode is one mapping key or sequence item ("-") of a block-style YAML
// document, with the scalar written after its colon.
type yamlNode struct {
	line  int
	key   string
	value string
	kids  []*yamlNode
}

// parseBlockYAML reads the block layout a workflow uses: mappings, sequences
// of mappings and scalars, with block scalars (| and >) skipped whole. A line
// it cannot read is an error, never a key it drops.
func parseBlockYAML(lines []string) (*yamlNode, error) {
	root := &yamlNode{}
	p := &yamlParser{stack: []yamlFrame{{-1, root}}}
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimLeft(lines[i], " ")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "\t") {
			return nil, fmt.Errorf("line %d: indented with a tab", i+1)
		}
		node, indent, err := p.entry(i+1, len(lines[i])-len(trimmed), trimmed)
		if err != nil {
			return nil, err
		}
		if node != nil && (strings.HasPrefix(node.value, "|") || strings.HasPrefix(node.value, ">")) {
			for i+1 < len(lines) && (strings.TrimSpace(lines[i+1]) == "" || len(lines[i+1])-len(strings.TrimLeft(lines[i+1], " ")) > indent) {
				i++
			}
		}
	}
	return root, nil
}

type yamlFrame struct {
	indent int
	node   *yamlNode
}

type yamlParser struct {
	stack []yamlFrame
}

// parent closes every open node at indent or deeper and returns the one a
// node at indent belongs to.
func (p *yamlParser) parent(indent int) *yamlNode {
	for p.stack[len(p.stack)-1].indent >= indent {
		p.stack = p.stack[:len(p.stack)-1]
	}
	return p.stack[len(p.stack)-1].node
}

func (p *yamlParser) open(indent int, node *yamlNode) {
	parent := p.parent(indent)
	parent.kids = append(parent.kids, node)
	p.stack = append(p.stack, yamlFrame{indent, node})
}

// entry places one line's sequence items and key, and returns the key node
// with the column its key starts at, or nil for a scalar item.
func (p *yamlParser) entry(line, indent int, text string) (*yamlNode, int, error) {
	for text == "-" || strings.HasPrefix(text, "- ") {
		item := &yamlNode{line: line, key: "-"}
		p.open(indent, item)
		rest := strings.TrimPrefix(text[1:], " ")
		text = strings.TrimLeft(rest, " ")
		indent += 2 + len(rest) - len(text)
		if !isYAMLKey(text) {
			item.value = stripYAMLComment(text)
			return nil, indent, nil
		}
	}
	if !isYAMLKey(text) {
		return nil, 0, fmt.Errorf("line %d: %q is not a key", line, text)
	}
	key, value, _ := strings.Cut(text, ":")
	node := &yamlNode{line: line, key: strings.TrimSpace(key), value: stripYAMLComment(value)}
	p.open(indent, node)
	return node, indent, nil
}

// isYAMLKey reports a "key:" or "key: value" line. A quoted scalar is
// never read as a key, though a quoted key is.
func isYAMLKey(text string) bool {
	key, value, ok := strings.Cut(text, ":")
	if !ok || key == "" || value != "" && !strings.HasPrefix(value, " ") {
		return false
	}
	if q := key[0]; q == '"' || q == '\'' {
		return len(key) > 1 && key[len(key)-1] == q
	}
	return true
}

func stripYAMLComment(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "#") {
		return ""
	}
	if i := strings.Index(value, " #"); i >= 0 && !strings.ContainsAny(value[:i], `'"`) {
		value = strings.TrimSpace(value[:i])
	}
	return value
}

func (n *yamlNode) children(key string) []*yamlNode {
	var found []*yamlNode
	for _, kid := range n.kids {
		if kid.key == key {
			found = append(found, kid)
		}
	}
	return found
}

// ciGateProblems names every way ci.yml stops proving that `make quality`
// ran and passed: the gate job is missing or doubled, carries a key that can
// skip it or mask its result, or has no step that runs exactly the gate and
// nothing else, unconditionally; or the workflow sets an environment or a
// default shell that changes what the step runs.
func ciGateProblems(lines []string) []string {
	root, err := parseBlockYAML(lines)
	if err != nil {
		return []string{err.Error()}
	}
	problems := workflowLevelProblems(root)
	var jobs []*yamlNode
	for _, j := range root.children("jobs") {
		jobs = append(jobs, j.children(ciGateJob)...)
	}
	if len(jobs) != 1 {
		return append(problems, fmt.Sprintf("found %d %s job(s) under jobs, want 1", len(jobs), ciGateJob))
	}
	job := jobs[0]
	problems = append(problems, aliasProblems(job)...)
	problems = append(problems, keyProblems(job, ciJobKeys, "the "+ciGateJob+" job")...)
	var gate []*yamlNode
	for _, steps := range job.children("steps") {
		for _, step := range steps.kids {
			if slices.ContainsFunc(step.kids, func(k *yamlNode) bool { return k.key == "run" && k.value == ciGateCommand }) {
				gate = append(gate, step)
			}
		}
	}
	switch len(gate) {
	case 0:
		problems = append(problems, fmt.Sprintf("no step of the %s job runs exactly %q", ciGateJob, ciGateCommand))
	case 1:
	default:
		problems = append(problems, fmt.Sprintf("%d steps of the %s job run %q, want 1", len(gate), ciGateJob, ciGateCommand))
	}
	for _, step := range gate {
		problems = append(problems, keyProblems(step, ciStepKeys, fmt.Sprintf("the step at line %d", step.line))...)
	}
	return problems
}

func workflowLevelProblems(root *yamlNode) []string {
	var problems []string
	for _, env := range root.children("env") {
		problems = append(problems, fmt.Sprintf("line %d: the workflow sets env, which reaches the gate step", env.line))
	}
	for _, defaults := range root.children("defaults") {
		for _, run := range defaults.kids {
			for _, kid := range run.kids {
				if run.key != "run" || kid.key != "shell" || kid.value != "bash" {
					problems = append(problems, fmt.Sprintf("line %d: the workflow default %s.%s is %q, want only run.shell: bash", kid.line, run.key, kid.key, kid.value))
				}
			}
			if run.key != "run" || len(run.kids) == 0 {
				problems = append(problems, fmt.Sprintf("line %d: the workflow default %s is not run.shell: bash", run.line, run.key))
			}
		}
	}
	return problems
}

// keyProblems refuses any key of n outside allowed, and any allowed key
// written twice.
func keyProblems(n *yamlNode, allowed []string, where string) []string {
	var problems []string
	var seen []string
	for _, kid := range n.kids {
		switch {
		case !slices.Contains(allowed, kid.key):
			problems = append(problems, fmt.Sprintf("line %d: %s carries %s", kid.line, where, kid.key))
		case slices.Contains(seen, kid.key):
			problems = append(problems, fmt.Sprintf("line %d: %s carries %s twice", kid.line, where, kid.key))
		}
		seen = append(seen, kid.key)
	}
	return problems
}

// aliasProblems refuses an alias or a merge key anywhere under n: either
// can bring in a key the allowlists above never see.
func aliasProblems(n *yamlNode) []string {
	var problems []string
	for _, kid := range n.kids {
		if kid.key == "<<" || strings.HasPrefix(kid.value, "*") || strings.HasPrefix(kid.key, "*") {
			problems = append(problems, fmt.Sprintf("line %d: an alias or merge key in the %s job", kid.line, ciGateJob))
		}
		problems = append(problems, aliasProblems(kid)...)
	}
	return problems
}

// TestCIRunsTheWholeGate pins what CI runs: TestQualityRunsTheWholeGate holds
// make quality to its targets, and this holds ci.yml to make quality.
func TestCIRunsTheWholeGate(t *testing.T) {
	for _, problem := range ciGateProblems(readLines(t, repoFS(t), ciWorkflowPath)) {
		t.Errorf("%s: %s", ciWorkflowPath, problem)
	}
}

const ciFixture = `name: CI

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

defaults:
  run:
    # a comment
    shell: bash

jobs:
  quality:
    name: Quality gate
    runs-on: ubuntu-24.04
    timeout-minutes: 30
    steps:
      - name: Check out the repository
        uses: actions/checkout@0123 # v1
        with:
          persist-credentials: false

      - name: Load pins
        # a comment
        run: |
          grep -E 'x' file >>"$GITHUB_ENV"
          if: this is script text, not a key
      - name: Run the quality gate
        run: make quality

  other:
    if: false
    continue-on-error: true
    steps:
      - run: make quality-quick
`

func TestCIGateProblems(t *testing.T) {
	if problems := ciGateProblems(strings.Split(ciFixture, "\n")); len(problems) != 0 {
		t.Fatalf("the correct fixture reported %q", problems)
	}
	gateStep := "      - name: Run the quality gate\n        run: make quality\n"
	jobHead := "    name: Quality gate\n"
	cases := map[string]struct{ old, replacement, want string }{
		"the quick gate":                {"run: make quality\n\n", "run: make quality-quick\n\n", `no step of the quality job runs exactly "make quality"`},
		"the gate with a fallback":      {"run: make quality\n\n", "run: make quality || true\n\n", "runs exactly"},
		"the gate in a block":           {"run: make quality\n\n", "run: |\n          make quality\n\n", "runs exactly"},
		"the gate quoted":               {"run: make quality\n\n", "run: \"make quality\"\n\n", "runs exactly"},
		"an if on the step":             {gateStep, gateStep + "        if: false\n", "carries if"},
		"an if before the run":          {gateStep, "      - name: Run the quality gate\n        if: false\n        run: make quality\n", "carries if"},
		"continue-on-error on a step":   {gateStep, gateStep + "        continue-on-error: true\n", "carries continue-on-error"},
		"a shell on the step":           {gateStep, gateStep + "        shell: true {0}\n", "carries shell"},
		"env on the step":               {gateStep, gateStep + "        env:\n          MAKEFLAGS: -n\n", "carries env"},
		"a working directory":           {gateStep, gateStep + "        working-directory: docs\n", "carries working-directory"},
		"the run written twice":         {gateStep, gateStep + "        run: make quality\n", "carries run twice"},
		"two gate steps":                {gateStep, gateStep + gateStep, `2 steps of the quality job run "make quality"`},
		"an if on the job":              {jobHead, jobHead + "    if: false\n", "the quality job carries if"},
		"continue-on-error on a job":    {jobHead, jobHead + "    continue-on-error: true\n", "the quality job carries continue-on-error"},
		"needs on the job":              {jobHead, jobHead + "    needs: other\n", "the quality job carries needs"},
		"a quoted if on the job":        {jobHead, jobHead + "    'if': false\n", "the quality job carries 'if'"},
		"a merge key on the job":        {jobHead, jobHead + "    <<: *skip\n", "an alias or merge key"},
		"a job default shell":           {jobHead, jobHead + "    defaults:\n      run:\n        shell: true {0}\n", "the quality job carries defaults"},
		"a workflow default shell":      {"    shell: bash\n", "    shell: true {0}\n", `the workflow default run.shell is "true {0}"`},
		"a workflow env":                {"permissions:\n", "env:\n  MAKEFLAGS: -i\npermissions:\n", "the workflow sets env"},
		"no gate job":                   {"  quality:\n", "  quality-renamed:\n", "found 0 quality job(s)"},
		"two gate jobs":                 {"  other:\n", "  quality:\n", "found 2 quality job(s)"},
		"a tab":                         {"    runs-on: ubuntu-24.04\n", "\truns-on: ubuntu-24.04\n", "indented with a tab"},
		"a plain scalar over two lines": {"    runs-on: ubuntu-24.04\n", "    runs-on: ubuntu-24.04\n      more text\n", `"more text" is not a key`},
	}
	for name, c := range cases {
		text := replaceOnce(t, ciFixture, c.old, c.replacement)
		problems := ciGateProblems(strings.Split(text, "\n"))
		if !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
}
