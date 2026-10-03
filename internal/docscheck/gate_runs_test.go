package docscheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	gateRunsSHA = "1111111111111111111111111111111111111111"
	gateMainSHA = "3333333333333333333333333333333333333333"
)

// stubGH answers `gh api <url> --jq <filter>` with the canned answer for the
// URL, evaluated with jq as gh would evaluate it, and logs every URL it was
// asked for. The branch and the comparison are matched exactly, so a request
// that names main any other way is refused.
const stubGH = `#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == api && "$3" == --jq && $# -eq 4 ]] || { echo "stub gh: unexpected arguments: $*" >&2; exit 64; }
printf '%s\n' "$2" >>"${STUB_DIR}/urls"
case "$2" in
  "repos/owner/name/git/ref/heads/main") answer="${STUB_DIR}/ref.json" ;;
  "repos/owner/name/compare/${STUB_MAIN}...${STUB_SHA}") answer="${STUB_DIR}/compare.json" ;;
  */workflows/ci.yml/runs\?*) answer="${STUB_DIR}/ci.json" ;;
  */workflows/security.yml/runs\?*) answer="${STUB_DIR}/security.json" ;;
  *) echo "stub gh: unexpected url $2" >&2; exit 64 ;;
esac
exec jq -r "$4" "${answer}"
`

// gateAnswers are the stub's answers; an empty ref or compare is replaced by
// main at gateMainSHA and a comparison that finds the commit identical.
type gateAnswers struct {
	ci, security, ref, compare string
}

func gateRun(sha, event, branch, conclusion string) string {
	field := func(v string) string {
		if v == "" {
			return "null"
		}
		return `"` + v + `"`
	}
	return `{"head_sha":` + field(sha) + `,"event":` + field(event) + `,"head_branch":` + field(branch) +
		`,"status":"completed","conclusion":` + field(conclusion) + `}`
}

func gateRuns(runs ...string) string {
	return gateRunsCounted(len(runs), runs...)
}

func gateRunsCounted(total int, runs ...string) string {
	return `{"total_count":` + strconv.Itoa(total) + `,"workflow_runs":[` + strings.Join(runs, ",") + `]}`
}

func gateCompare(status string) string {
	return `{"status":"` + status + `","ahead_by":0,"behind_by":0}`
}

func TestCheckGateRunsReadsTheRunItCounts(t *testing.T) {
	bash, script := gateRunsTools(t)

	good := gateRuns(gateRun(gateRunsSHA, "push", "main", "success"))
	cases := map[string]struct {
		ci   string
		pass bool
		want string
	}{
		"a successful push run on main": {good, true, "security.yml passed on " + gateRunsSHA},
		"a push run beside a pull request run": {
			gateRuns(gateRun(gateRunsSHA, "pull_request", "feature", "failure"), gateRun(gateRunsSHA, "push", "main", "success")),
			true, "ci.yml passed on " + gateRunsSHA,
		},
		"only a pull_request run": {
			gateRuns(gateRun(gateRunsSHA, "pull_request", "feature", "success")), false,
			"ci.yml has no successful push run on main for " + gateRunsSHA + "; GitHub listed: a pull_request run on feature (conclusion success)",
		},
		"a pull_request run from a fork's main": {
			gateRuns(gateRun(gateRunsSHA, "pull_request", "main", "success")), false,
			"GitHub listed: a pull_request run on main (conclusion success)",
		},
		"a push run on another branch": {
			gateRuns(gateRun(gateRunsSHA, "push", "feature", "success")), false,
			"GitHub listed: a push run on feature (conclusion success)",
		},
		"a successful push run of another commit": {
			gateRuns(gateRun("2222222222222222222222222222222222222222", "push", "main", "success")), false,
			"GitHub listed: no run of this commit; 1 run(s) of other commits",
		},
		"a failed push run": {
			gateRuns(gateRun(gateRunsSHA, "push", "main", "failure")), false,
			"GitHub listed: a push run on main (conclusion failure)",
		},
		"a skipped push run": {
			gateRuns(gateRun(gateRunsSHA, "push", "main", "skipped")), false,
			"GitHub listed: a push run on main (conclusion skipped)",
		},
		"a neutral push run": {
			gateRuns(gateRun(gateRunsSHA, "push", "main", "neutral")), false,
			"GitHub listed: a push run on main (conclusion neutral)",
		},
		"a push run still in progress": {
			gateRuns(gateRun(gateRunsSHA, "push", "main", "")), false,
			"GitHub listed: a push run on main (conclusion none)",
		},
		"an empty list": {gateRuns(), false, "ci.yml has no successful push run on main for " + gateRunsSHA + "; GitHub listed: no run of this commit"},
		"more runs than one page, the passing one among those read": {
			gateRunsCounted(101, gateRun(gateRunsSHA, "push", "main", "success")), false,
			"ci.yml: GitHub counts 101 run(s) of " + gateRunsSHA + " and answered 1; more runs than one page; refusing rather than guessing",
		},
		"an answer with no total_count": {
			`{"workflow_runs":[` + gateRun(gateRunsSHA, "push", "main", "success") + `]}`, false,
			"could not read ci.yml's runs on " + gateRunsSHA,
		},
		"malformed JSON":                {`{"workflow_runs":[`, false, "could not read ci.yml's runs on " + gateRunsSHA},
		"an answer with no runs array":  {`{"message":"Not Found"}`, false, "could not read ci.yml's runs on " + gateRunsSHA},
		"a runs field that is no array": {`{"total_count":1,"workflow_runs":{"head_sha":"` + gateRunsSHA + `"}}`, false, "could not read ci.yml's runs"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, urls, err := runCheckGateRuns(t, bash, script, gateAnswers{ci: tc.ci, security: good})
			checkGateVerdict(t, out, err, tc.pass, tc.want)
			want := "repos/owner/name/actions/workflows/ci.yml/runs?head_sha=" + gateRunsSHA + "&event=push&branch=main&per_page=100"
			if !slices.Contains(urls, want) {
				t.Errorf("the script never asked for %q; it asked for %q", want, urls)
			}
		})
	}

	t.Run("Security failed where CI passed", func(t *testing.T) {
		out, _, err := runCheckGateRuns(t, bash, script, gateAnswers{ci: good, security: gateRuns(gateRun(gateRunsSHA, "push", "main", "cancelled"))})
		checkGateVerdict(t, out, err, false, "security.yml has no successful push run on main for "+gateRunsSHA)
	})
}

// A run whose head_branch is main is not enough: a push of a tag named main
// carries that head_branch too, for any commit. The commit itself has to be
// on main's branch head.
func TestCheckGateRunsRefusesACommitNotOnMain(t *testing.T) {
	bash, script := gateRunsTools(t)

	good := gateRuns(gateRun(gateRunsSHA, "push", "main", "success"))
	ref := `{"ref":"refs/heads/main","object":{"sha":"` + gateMainSHA + `","type":"commit"}}`
	cases := map[string]struct {
		ref, compare string
		pass         bool
		want         string
	}{
		"main's head itself":      {ref, gateCompare("identical"), true, "security.yml passed on " + gateRunsSHA},
		"an ancestor of main":     {ref, gateCompare("behind"), true, "security.yml passed on " + gateRunsSHA},
		"a commit ahead of main":  {ref, gateCompare("ahead"), false, gateRunsSHA + " is not on main: compared with main's head " + gateMainSHA + ", GitHub says 'ahead'"},
		"a commit beside main":    {ref, gateCompare("diverged"), false, "is not on main: compared with main's head " + gateMainSHA + ", GitHub says 'diverged'"},
		"a comparison not found":  {ref, `{"message":"Not Found"}`, false, "could not compare " + gateRunsSHA + " with main's head " + gateMainSHA},
		"a malformed comparison":  {ref, `{"status":`, false, "could not compare " + gateRunsSHA + " with main's head"},
		"main not found":          {`{"message":"Not Found"}`, gateCompare("identical"), false, "could not read the head of main"},
		"a list of matching refs": {`[` + ref + `]`, gateCompare("identical"), false, "could not read the head of main"},
		"another branch's ref": {
			`{"ref":"refs/heads/main-old","object":{"sha":"` + gateMainSHA + `","type":"commit"}}`, gateCompare("identical"), false,
			"could not read the head of main",
		},
		"a head that is no commit id": {
			`{"ref":"refs/heads/main","object":{"sha":"main","type":"commit"}}`, gateCompare("identical"), false,
			"GitHub answered 'main' for the head of main",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, _, err := runCheckGateRuns(t, bash, script, gateAnswers{ci: good, security: good, ref: tc.ref, compare: tc.compare})
			checkGateVerdict(t, out, err, tc.pass, tc.want)
		})
	}
}

func gateRunsTools(t *testing.T) (bash, script string) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("bash is not on PATH, so scripts/check-gate-runs.sh cannot be tested: %v", err)
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Fatalf("jq is not on PATH, and the stub gh evaluates the script's --jq filter with it: %v", err)
	}
	return bash, filepath.Join(repoRoot(t), "scripts", "check-gate-runs.sh")
}

func checkGateVerdict(t *testing.T, out string, err error, pass bool, want string) {
	t.Helper()
	if pass && err != nil {
		t.Fatalf("refused, want a pass: %v\n%s", err, out)
	}
	if !pass && err == nil {
		t.Fatalf("passed, want a refusal:\n%s", out)
	}
	if !strings.Contains(out, want) {
		t.Errorf("output does not say %q:\n%s", want, out)
	}
}

// runCheckGateRuns runs the script with the stub gh first on PATH and returns
// its output, the URLs the stub was asked for in order, and its error.
func runCheckGateRuns(t *testing.T, bash, script string, a gateAnswers) (string, []string, error) {
	t.Helper()
	if a.ref == "" {
		a.ref = `{"ref":"refs/heads/main","object":{"sha":"` + gateMainSHA + `","type":"commit"}}`
	}
	if a.compare == "" {
		a.compare = gateCompare("identical")
	}
	dir := t.TempDir()
	for file, content := range map[string]string{"ci.json": a.ci, "security.json": a.security, "ref.json": a.ref, "compare.json": a.compare} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(stubGH), 0o700); err != nil { //nolint:gosec // G306: the stub has to be executable
		t.Fatal(err)
	}
	cmd := exec.Command(bash, script, gateRunsSHA) //nolint:gosec // G204: bash from PATH running this repository's own script
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"STUB_DIR="+dir,
		"STUB_MAIN="+gateMainSHA,
		"STUB_SHA="+gateRunsSHA,
		"GITHUB_REPOSITORY=owner/name",
	)
	out, runErr := cmd.CombinedOutput()
	urls, err := os.ReadFile(filepath.Join(dir, "urls")) //nolint:gosec // G304: the test's own temporary directory
	if err != nil {
		t.Fatalf("the stub gh was never asked: %v\n%s", err, out)
	}
	return string(out), strings.Fields(string(urls)), runErr
}
