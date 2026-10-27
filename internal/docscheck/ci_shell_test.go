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
	nightlyFuzzCommand  = "/usr/bin/env -u MAKEFLAGS -u GNUMAKEFLAGS -u MFLAGS -u MAKEFILES make fuzz-smoke"
)

// shellProblems holds one workflow to ciShell: a workflow with a run step
// sets it as its default, and nothing sets another shell, at the workflow,
// a job or a step. Every required check runs in some workflow's steps, so an
// earlier step's $GITHUB_ENV write reaches none of their shells.
func shellProblems(lines []string) []string {
	root, err := parseBlockYAML(lines)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	runs, defaulted := false, false
	var walk func(n *yamlNode, path []string)
	walk = func(n *yamlNode, path []string) {
		for _, kid := range n.kids {
			at := append(slices.Clone(path), kid.key)
			switch {
			case kid.key == "run" && slices.Contains(path, "steps"):
				runs = true
			case kid.key == "shell" && slices.Equal(at, []string{"defaults", "run", "shell"}) && kid.value == ciShell:
				defaulted = true
			case kid.key == "shell":
				problems = append(problems, fmt.Sprintf("line %d: %s is %q; every run step starts in the workflow default %s", kid.line, strings.Join(at, "."), kid.value, ciShell))
			}
			walk(kid, at)
		}
	}
	walk(root, nil)
	if runs && !defaulted {
		problems = append(problems, "the workflow has run steps and no default run.shell: "+ciShell)
	}
	return problems
}

func TestEveryWorkflowStartsItsShellClean(t *testing.T) {
	paths, err := fs.Glob(repoFS(t), ".github/workflows/*.yml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no workflow found (%v); refusing to report a pass", err)
	}
	for _, path := range paths {
		for _, problem := range shellProblems(readLines(t, repoFS(t), path)) {
			t.Errorf("%s: %s", path, problem)
		}
	}
}

func TestShellProblems(t *testing.T) {
	const good = `on: push
defaults:
  run:
    shell: bash --noprofile --norc -p -e -o pipefail {0}
jobs:
  a:
    steps:
      - run: true
`
	if problems := shellProblems(strings.Split(good, "\n")); len(problems) != 0 {
		t.Fatalf("the correct fixture reported %q", problems)
	}
	for name, c := range map[string]struct{ old, replacement, want string }{
		"GitHub's own bash":  {"    shell: " + ciShell + "\n", "    shell: bash\n", "no default run.shell"},
		"no defaults":        {"defaults:\n  run:\n    shell: " + ciShell + "\n", "", "no default run.shell"},
		"a step's own shell": {"      - run: true\n", "      - run: true\n        shell: bash\n", "jobs.a.steps.-.shell is \"bash\""},
		"a job default":      {"    steps:\n", "    defaults:\n      run:\n        shell: sh\n    steps:\n", "jobs.a.defaults.run.shell"},
	} {
		problems := shellProblems(strings.Split(replaceOnce(t, good, c.old, c.replacement), "\n"))
		if !slices.ContainsFunc(problems, func(s string) bool { return strings.Contains(s, c.want) }) {
			t.Errorf("%s: want a problem containing %q, got %q", name, c.want, problems)
		}
	}
	noRun := "on: push\njobs:\n  a:\n    steps:\n      - uses: x/y@0123 # v1\n"
	if problems := shellProblems(strings.Split(noRun, "\n")); len(problems) != 0 {
		t.Errorf("a workflow with no run step reported %q", problems)
	}
}

// TestTheShellIgnoresTheStartupStateOfTheEnvironment runs the launcher every
// workflow names over a script that prints and exits 7, under each variable
// an earlier step could write through $GITHUB_ENV to change what bash runs;
// on CI it runs against the runner's own bash. Each case first shows that
// GitHub's plain bash is changed by the same variable, so a case cannot pass
// because the variable never reached the shell.
func TestTheShellIgnoresTheStartupStateOfTheEnvironment(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("the workflows run bash: %v", err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "step.sh")
	startup := filepath.Join(dir, "startup.sh")
	for name, body := range map[string]string{script: "echo ran\nexit 7\n", startup: "echo startup ran\n"} {
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	args, ok := strings.CutPrefix(strings.Replace(ciShell, "{0}", script, 1), "bash ")
	if !ok {
		t.Fatalf("ciShell %q does not start bash", ciShell)
	}
	run := func(env string, args ...string) (string, int) {
		cmd := exec.Command(bash, args...) //nolint:gosec // G204: bash over a script this test wrote
		cmd.Env = append(cleanMakeEnv(), env)
		out, err := cmd.CombinedOutput()
		if err == nil {
			return string(out), 0
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("bash %v: %v", args, err)
		}
		return string(out), exit.ExitCode()
	}
	plain := []string{"--noprofile", "--norc", "-e", "-o", "pipefail", script}
	for _, env := range []string{
		"SHELLOPTS=noexec",
		"BASH_ENV=" + startup,
		"BASH_FUNC_echo%%=() { builtin printf 'a function ran\\n'; }",
	} {
		if out, code := run(env, plain...); out == "ran\n" && code == 7 {
			t.Errorf("plain bash under %s ran the step unchanged; this case cannot tell the launcher's -p from a variable that never arrived", env)
		}
		if out, code := run(env, strings.Fields(args)...); out != "ran\n" || code != 7 {
			t.Errorf("%s under %s = %d, %q; want 7 and the step's own line", ciShell, env, code, out)
		}
	}
}

// TestFuzzingRunsOutsideTheGate holds the two places ADR-0062 moved the fuzz
// run to: one unconditional step of the nightly workflow, and the cold gate
// of a release commit.
func TestFuzzingRunsOutsideTheGate(t *testing.T) {
	root, err := parseBlockYAML(readLines(t, repoFS(t), nightlyWorkflowPath))
	if err != nil {
		t.Fatal(err)
	}
	if found := unconditionalSteps(root, nightlyFuzzCommand); found != 1 {
		t.Errorf("%s: %d unconditional step(s) run %q, want 1", nightlyWorkflowPath, found, nightlyFuzzCommand)
	}
	gate, err := fs.ReadFile(repoFS(t), "scripts/gate-commit.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gate), `make GO=go fuzz-smoke)`) {
		t.Errorf("scripts/gate-commit.sh does not run make fuzz-smoke on the release commit's archive")
	}
}

// unconditionalSteps counts the steps that run exactly command in a job with
// no if, with neither an if nor continue-on-error of their own.
func unconditionalSteps(root *yamlNode, command string) int {
	found := 0
	for _, jobs := range root.children("jobs") {
		for _, job := range jobs.kids {
			if len(job.children("if")) != 0 {
				continue
			}
			for _, steps := range job.children("steps") {
				for _, step := range steps.kids {
					runs := slices.ContainsFunc(step.kids, func(k *yamlNode) bool { return k.key == "run" && k.value == command })
					if runs && len(step.children("if")) == 0 && len(step.children("continue-on-error")) == 0 {
						found++
					}
				}
			}
		}
	}
	return found
}
