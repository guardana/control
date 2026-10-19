package docscheck

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// requiredCheckIf is the one condition a required check's job may carry: it
// holds on every pull request, where the check is required.
const requiredCheckIf = "github.event_name == 'pull_request'"

// workflowJob is one job of a workflow, keyed elsewhere by the check-run
// context it reports: its name, or its id when it has none.
type workflowJob struct {
	where   string
	node    *yamlNode
	everyPR bool
}

// jobsOf reads one workflow's jobs by the context each reports.
func jobsOf(file string, lines []string, into map[string][]workflowJob) error {
	doc, err := parseBlockYAML(lines)
	if err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	everyPR := runsOnEveryPullRequest(doc)
	for _, jobs := range doc.children("jobs") {
		for _, job := range jobs.kids {
			context := job.key
			if names := job.children("name"); len(names) == 1 {
				context = unquote(names[0].value)
			}
			where := fmt.Sprintf("%s:%d", file, job.line)
			into[context] = append(into[context], workflowJob{where: where, node: job, everyPR: everyPR})
		}
	}
	return nil
}

// runsOnEveryPullRequest reports whether doc's on: starts it for every pull
// request and every push to one: a pull_request trigger with no filter but
// activity types that include opened, reopened and synchronize.
func runsOnEveryPullRequest(doc *yamlNode) bool {
	ons := doc.children("on")
	if len(ons) != 1 {
		return false
	}
	if ons[0].value != "" {
		return slices.Contains(flowList(ons[0].value), "pull_request")
	}
	triggers := ons[0].children("pull_request")
	if len(triggers) != 1 || triggers[0].value != "" {
		return false
	}
	for _, filter := range triggers[0].kids {
		types := sequence(filter)
		if filter.key != "types" || !slices.Contains(types, "opened") ||
			!slices.Contains(types, "reopened") || !slices.Contains(types, "synchronize") {
			return false
		}
	}
	return true
}

// requiredCheckProblems names each required check no job reports, or more
// than one does, or whose job a pull request can leave unreported or skip:
// its workflow does not run on every pull request, or the job waits on
// another or carries a condition other than requiredCheckIf.
func requiredCheckProblems(checks []string, jobs map[string][]workflowJob) []string {
	var problems []string
	for _, check := range checks {
		found := jobs[check]
		if len(found) != 1 {
			problems = append(problems, fmt.Sprintf("%d jobs report the required check %q, want 1", len(found), check))
			continue
		}
		job := found[0]
		if !job.everyPR {
			problems = append(problems, fmt.Sprintf("%s: the workflow of %q does not run on every pull request", job.where, check))
		}
		if len(job.node.children("needs")) != 0 {
			problems = append(problems, fmt.Sprintf("%s: %q waits on another job, which can skip it", job.where, check))
		}
		for _, cond := range job.node.children("if") {
			expr := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(cond.value, "${{"), "}}"))
			if expr != requiredCheckIf {
				problems = append(problems, fmt.Sprintf("%s: %q runs only if %s", job.where, check, cond.value))
			}
		}
	}
	return problems
}

// TestEveryRequiredCheckIsAJobEveryPullRequestRuns: a required check no job
// reports blocks every merge until an admin edits the ruleset, and one whose
// job is skipped reads as passed. The bootstrap refuses the first before it
// applies anything; this holds the list to the workflows on every change.
func TestEveryRequiredCheckIsAJobEveryPullRequestRuns(t *testing.T) {
	root := repoRoot(t)
	paths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no workflow: %v", err)
	}
	jobs := map[string][]workflowJob{}
	for _, path := range paths {
		raw, err := os.ReadFile(path) //nolint:gosec // G304: a workflow this repository keeps
		if err != nil {
			t.Fatal(err)
		}
		if err := jobsOf(filepath.Base(path), strings.Split(string(raw), "\n"), jobs); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range requiredCheckProblems(settingsValues(t, `printf '%s\n' "${REQUIRED_CHECKS[@]}"`), jobs) {
		t.Error(p)
	}
}

// Each planted workflow breaks the rule one way, and only that way.
func TestARequiredCheckAPullRequestCanMissIsCaught(t *testing.T) {
	const good = "on:\n  pull_request:\njobs:\n  gate:\n    name: Gate\n    runs-on: ubuntu-latest\n"
	for name, doc := range map[string]string{
		"a renamed job":           strings.Replace(good, "name: Gate", "name: Gates", 1),
		"no name, another id":     strings.Replace(good, "    name: Gate\n", "", 1),
		"a path filter":           strings.Replace(good, "  pull_request:\n", "  pull_request:\n    paths: [docs/**]\n", 1),
		"no synchronize":          strings.Replace(good, "  pull_request:\n", "  pull_request:\n    types: [opened, reopened]\n", 1),
		"no pull request trigger": strings.Replace(good, "  pull_request:\n", "  push:\n", 1),
		"a condition":             good + "    if: github.event_name == 'push'\n",
		"a job it waits on":       good + "    needs: build\n",
		"two jobs of the name":    good + "  again:\n    name: Gate\n",
	} {
		jobs := map[string][]workflowJob{}
		if err := jobsOf("planted.yml", strings.Split(doc, "\n"), jobs); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := requiredCheckProblems([]string{"Gate"}, jobs); len(got) != 1 {
			t.Errorf("%s: %q, want one problem", name, got)
		}
	}
	for name, doc := range map[string]string{
		"a bare trigger":             "on:\n  pull_request:\njobs:\n  gate:\n    name: Gate\n",
		"a flow list":                "on: [push, pull_request]\njobs:\n  gate:\n    name: Gate\n",
		"every activity it needs":    "on:\n  pull_request:\n    types: [opened, edited, reopened, synchronize]\njobs:\n  gate:\n    name: Gate\n",
		"the pull request condition": "on:\n  pull_request:\njobs:\n  gate:\n    name: Gate\n    if: ${{ " + requiredCheckIf + " }}\n",
	} {
		jobs := map[string][]workflowJob{}
		if err := jobsOf("planted.yml", strings.Split(doc, "\n"), jobs); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := requiredCheckProblems([]string{"Gate"}, jobs); len(got) != 0 {
			t.Errorf("%s: %q, want none", name, got)
		}
	}
}
