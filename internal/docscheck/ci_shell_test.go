package docscheck

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	nightlyWorkflowPath = ".github/workflows/nightly.yml"
	nightlyFuzzJob      = "fuzz"
	nightlyFuzzCommand  = "/usr/bin/env -u MAKEFLAGS -u GNUMAKEFLAGS -u MFLAGS -u MAKEFILES make fuzz-smoke"
	// gateCommitFuzzLine is the line of scripts/gate-commit.sh that fuzzes
	// the release commit's archive.
	gateCommitFuzzLine = `(cd "${tree}" && "${clean[@]}" make GO=go fuzz-smoke) >>"${log}" 2>&1 || rc=$?`
)

// The shell startup state ciShell removes from a step's environment.
var ciShellCleared = []string{"BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS", "CDPATH", "GLOBIGNORE"}

// unquotedKey is a key as YAML reads it, quoted or not.
func unquotedKey(key string) string {
	if len(key) >= 2 && (key[0] == '"' || key[0] == '\'') && key[len(key)-1] == key[0] {
		return key[1 : len(key)-1]
	}
	return key
}

// shellProblems holds one workflow or composite action to ciShell. A
// workflow with a run step sets it as its default and nothing sets another
// shell, at the workflow, a job or a step; every run step of a composite
// action carries it, since an action has no default. A step written as a flow
// mapping is refused: this check cannot read one.
func shellProblems(lines []string, action bool) []string {
	root, err := parseBlockYAML(lines)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	runs, defaulted := false, false
	var walk func(n *yamlNode, path []string)
	walk = func(n *yamlNode, path []string) {
		for _, kid := range n.kids {
			key := unquotedKey(kid.key)
			at := append(slices.Clone(path), key)
			switch {
			case strings.HasPrefix(key, "{") || strings.HasPrefix(kid.value, "{"):
				problems = append(problems, fmt.Sprintf("line %d: a flow mapping, which this check cannot read", kid.line))
			case key == "run" && slices.Contains(path, "steps"):
				runs = true
				if action && !slices.ContainsFunc(n.kids, func(k *yamlNode) bool { return unquotedKey(k.key) == "shell" }) {
					problems = append(problems, fmt.Sprintf("line %d: a run step of an action carries no shell: %s", kid.line, ciShell))
				}
			case key == "shell" && kid.value == ciShell && (action || slices.Equal(at, []string{"defaults", "run", "shell"})):
				defaulted = true
			case key == "shell":
				problems = append(problems, fmt.Sprintf("line %d: %s is %q; every run step starts in %s", kid.line, strings.Join(at, "."), kid.value, ciShell))
			}
			walk(kid, at)
		}
	}
	walk(root, nil)
	if runs && !defaulted && !action {
		problems = append(problems, "the workflow has run steps and no default run.shell: "+ciShell)
	}
	return problems
}

func TestEveryWorkflowStartsItsShellClean(t *testing.T) {
	var workflows, actions []string
	for _, pattern := range []string{".github/workflows/*.yml", ".github/workflows/*.yaml"} {
		found, err := fs.Glob(repoFS(t), pattern)
		if err != nil {
			t.Fatal(err)
		}
		workflows = append(workflows, found...)
	}
	for _, pattern := range []string{".github/actions/*/action.yml", ".github/actions/*/action.yaml"} {
		found, err := fs.Glob(repoFS(t), pattern)
		if err != nil {
			t.Fatal(err)
		}
		actions = append(actions, found...)
	}
	if len(workflows) == 0 {
		t.Fatal("no workflow found; refusing to report a pass")
	}
	for _, path := range slices.Concat(workflows, actions) {
		for _, problem := range shellProblems(readLines(t, repoFS(t), path), slices.Contains(actions, path)) {
			t.Errorf("%s: %s", path, problem)
		}
	}
}

func TestShellProblems(t *testing.T) {
	good := "on: push\ndefaults:\n  run:\n    shell: " + ciShell + "\njobs:\n  a:\n    steps:\n      - run: true\n"
	if problems := shellProblems(strings.Split(good, "\n"), false); len(problems) != 0 {
		t.Fatalf("the correct fixture reported %q", problems)
	}
	for name, c := range map[string]struct{ old, replacement, want string }{
		"GitHub's own bash":  {"    shell: " + ciShell + "\n", "    shell: bash\n", "no default run.shell"},
		"no defaults":        {"defaults:\n  run:\n    shell: " + ciShell + "\n", "", "no default run.shell"},
		"a step's own shell": {"      - run: true\n", "      - run: true\n        shell: bash\n", "jobs.a.steps.-.shell is \"bash\""},
		"a quoted shell key": {"      - run: true\n", "      - run: true\n        \"shell\": bash\n", "jobs.a.steps.-.shell is \"bash\""},
		"a job default":      {"    steps:\n", "    defaults:\n      run:\n        shell: sh\n    steps:\n", "jobs.a.defaults.run.shell"},
		"a flow step":        {"      - run: true\n", "      - {run: true, shell: sh}\n", "a flow mapping"},
	} {
		problems := shellProblems(strings.Split(replaceOnce(t, good, c.old, c.replacement), "\n"), false)
		if !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
	if problems := shellProblems(strings.Split("on: push\njobs:\n  a:\n    steps:\n      - uses: x/y@0123 # v1\n", "\n"), false); len(problems) != 0 {
		t.Errorf("a workflow with no run step reported %q", problems)
	}
	action := "runs:\n  using: composite\n  steps:\n    - run: true\n      shell: " + ciShell + "\n"
	if problems := shellProblems(strings.Split(action, "\n"), true); len(problems) != 0 {
		t.Errorf("a correct action reported %q", problems)
	}
	if problems := shellProblems(strings.Split(replaceOnce(t, action, "      shell: "+ciShell+"\n", "      shell: bash\n"), "\n"), true); len(problems) == 0 {
		t.Error("an action step in GitHub's own bash reported nothing")
	}
}

// runShell runs args with env added to a clean environment and returns what
// it printed and its exit status.
func runShell(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(args[0], args[1:]...) //nolint:gosec // G204: a shell over a script this test wrote
	cmd.Env = append(cleanMakeEnv(), env...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	}
	t.Fatalf("%v: %v", args, err)
	return "", 0
}

// TestTheShellIgnoresTheStartupStateOfTheEnvironment runs the shell line
// every workflow names over scripts that print and exit 7, under what an
// earlier step could write through $GITHUB_ENV to change what bash runs; on
// CI it runs against the runner's own bash. Each case first shows that
// GitHub's plain bash line is changed by the same input, so a case cannot pass
// because the input never reached the shell. A function exported in the
// environment is ignored by the step's own bash; a bash the step starts in
// turn still imports it.
func TestTheShellIgnoresTheStartupStateOfTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	own := write("own.sh", "echo ran\nexit 7\n")
	child := write("child.sh", "echo ran\n/usr/bin/env bash -c 'echo child'\nexit 7\n")
	listing := write("listing.sh", "/usr/bin/env\nexit 7\n")
	startup := write("startup.sh", "echo startup ran\n")
	line := func(script string) []string { return strings.Fields(strings.Replace(ciShell, "{0}", script, 1)) }
	plain := func(script string) []string {
		return []string{"/usr/bin/env", "bash", "--noprofile", "--norc", "-e", "-o", "pipefail", script}
	}
	for _, c := range []struct {
		env    string
		script string
		want   string
	}{
		{"SHELLOPTS=noexec", child, "ran\nchild\n"},
		{"BASH_ENV=" + startup, child, "ran\nchild\n"},
		{"BASH_FUNC_echo%%=() { builtin printf 'a function ran\\n'; }", own, "ran\n"},
	} {
		if out, code := runShell(t, []string{c.env}, plain(c.script)...); out == c.want && code == 7 {
			t.Errorf("plain bash under %s ran the step unchanged; this case cannot tell the shell line from an input that never arrived", c.env)
		}
		if out, code := runShell(t, []string{c.env}, line(c.script)...); out != c.want || code != 7 {
			t.Errorf("%s under %s = %d, %q; want 7 and %q", ciShell, c.env, code, out, c.want)
		}
	}
	var env []string
	for _, name := range ciShellCleared {
		env = append(env, name+"="+startup)
	}
	plainOut, _ := runShell(t, env, plain(listing)...)
	lineOut, code := runShell(t, env, line(listing)...)
	if code != 7 {
		t.Errorf("%s over a listing of the environment exited %d, want 7:\n%s", ciShell, code, lineOut)
	}
	for _, name := range ciShellCleared {
		if !holdsVariable(plainOut, name) {
			t.Errorf("plain bash's environment holds no %s; this case cannot show the shell line removes it", name)
		}
		if holdsVariable(lineOut, name) {
			t.Errorf("%s leaves %s in the step's environment, where every bash the step starts reads it", ciShell, name)
		}
	}
}

// holdsVariable reports whether a listing of the environment sets name.
func holdsVariable(listing, name string) bool {
	return strings.HasPrefix(listing, name+"=") || strings.Contains(listing, "\n"+name+"=")
}

// nightlyProblems names every way the nightly workflow stops fuzzing each
// night: no schedule, no fuzz job, a fuzz job that can be skipped, masked or
// handed another FUZZTIME, no unconditional step running exactly the fuzz
// command, or a red night's report that no longer waits for the fuzz job.
func nightlyProblems(lines []string) []string {
	root, err := parseBlockYAML(lines)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	if !slices.ContainsFunc(root.children("on"), func(on *yamlNode) bool { return len(on.children("schedule")) == 1 }) {
		problems = append(problems, "the workflow runs on no schedule")
	}
	for _, env := range root.children("env") {
		problems = append(problems, fmt.Sprintf("line %d: the workflow sets env, which reaches the fuzz step", env.line))
	}
	var jobs []*yamlNode
	for _, all := range root.children("jobs") {
		jobs = append(jobs, all.children(nightlyFuzzJob)...)
		for _, report := range all.children("report") {
			for _, needs := range report.children("needs") {
				if !slices.Contains(flowList(needs.value), nightlyFuzzJob) {
					problems = append(problems, fmt.Sprintf("line %d: the report of a red night needs %s, not the %s job", needs.line, needs.value, nightlyFuzzJob))
				}
			}
		}
	}
	if len(jobs) != 1 {
		return append(problems, fmt.Sprintf("found %d %s job(s), want 1", len(jobs), nightlyFuzzJob))
	}
	for _, key := range []string{"if", "continue-on-error", "env", "strategy", "needs", "defaults", "container"} {
		for _, kid := range jobs[0].children(key) {
			problems = append(problems, fmt.Sprintf("line %d: the %s job carries %s", kid.line, nightlyFuzzJob, key))
		}
	}
	if found := plainSteps(jobs[0], nightlyFuzzCommand); found != 1 {
		problems = append(problems, fmt.Sprintf("%d step(s) of the %s job run exactly %q with no if, continue-on-error or env, want 1", found, nightlyFuzzJob, nightlyFuzzCommand))
	}
	return problems
}

// plainSteps counts the steps of job that run exactly command with no if,
// continue-on-error or env of their own.
func plainSteps(job *yamlNode, command string) int {
	found := 0
	for _, steps := range job.children("steps") {
		for _, step := range steps.kids {
			runs := slices.ContainsFunc(step.kids, func(k *yamlNode) bool { return k.key == "run" && k.value == command })
			if runs && len(step.children("if")) == 0 && len(step.children("continue-on-error")) == 0 && len(step.children("env")) == 0 {
				found++
			}
		}
	}
	return found
}

// TestFuzzingRunsOutsideTheGate holds the two places ADR-0062 moved the fuzz
// run to: the nightly workflow, and the cold gate of a release commit.
func TestFuzzingRunsOutsideTheGate(t *testing.T) {
	for _, problem := range nightlyProblems(readLines(t, repoFS(t), nightlyWorkflowPath)) {
		t.Errorf("%s: %s", nightlyWorkflowPath, problem)
	}
	if !slices.ContainsFunc(readLines(t, repoFS(t), "scripts/gate-commit.sh"), func(l string) bool { return strings.TrimSpace(l) == gateCommitFuzzLine }) {
		t.Errorf("scripts/gate-commit.sh holds no line %q, which fuzzes the release commit's archive", gateCommitFuzzLine)
	}
}

func TestNightlyProblems(t *testing.T) {
	good := "on:\n  schedule:\n    - cron: \"1 2 * * *\"\njobs:\n  fuzz:\n    runs-on: x\n    steps:\n      - run: " + nightlyFuzzCommand +
		"\n  report:\n    needs: [race, fuzz]\n"
	if problems := nightlyProblems(strings.Split(good, "\n")); len(problems) != 0 {
		t.Fatalf("the correct fixture reported %q", problems)
	}
	step := "      - run: " + nightlyFuzzCommand + "\n"
	for name, c := range map[string]struct{ old, replacement, want string }{
		"no schedule":             {"  schedule:\n    - cron: \"1 2 * * *\"\n", "  workflow_dispatch:\n", "no schedule"},
		"a fuzz time on the step": {step, step + "        env:\n          FUZZTIME: 1ns\n", "want 1"},
		"a fuzz time on the job":  {"    runs-on: x\n", "    runs-on: x\n    env:\n      FUZZTIME: 1ns\n", "carries env"},
		"masked on the job":       {"    runs-on: x\n", "    runs-on: x\n    continue-on-error: true\n", "carries continue-on-error"},
		"a report that waits not": {"needs: [race, fuzz]", "needs: race", "not the fuzz job"},
		"another command":         {step, "      - run: make fuzz-smoke FUZZTIME=1ns\n", "want 1"},
	} {
		problems := nightlyProblems(strings.Split(replaceOnce(t, good, c.old, c.replacement), "\n"))
		if !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
}
