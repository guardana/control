package docscheck

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The fakes of scripts/draft-release.sh: gh logs every call and answers each
// endpoint the script asks from the files and counters under FAKE_DIR, and
// sleep returns at once. The API's read-back and the upload host's probe fail
// their first FAKE_GET_FAILS and FAKE_PROBE_FAILS calls, as a draft that is
// not served yet does.
const (
	fakeDraftGh = `#!/usr/bin/env bash
printf '%s\n' "$*" >>"${FAKE_DIR}/gh"
[[ "${1:-}" == api ]] || { echo "fake gh: not an api call: $*" >&2; exit 64; }
shift
method=GET; endpoint=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --method) method="$2"; shift 2 ;;
    --jq|-f|-F|-H|--input) shift 2 ;;
    --paginate) shift ;;
    -*) echo "fake gh: refusing $1" >&2; exit 64 ;;
    *) [[ -z "${endpoint}" ]] || { echo "fake gh: two endpoints" >&2; exit 64; }; endpoint="$1"; shift ;;
  esac
done
count() { local f="${FAKE_DIR}/count-$1" n=0; [[ -f "${f}" ]] && n="$(cat "${f}")"; n=$((n+1)); echo "${n}" >"${f}"; echo "${n}"; }
case "${method} ${endpoint}" in
  "GET repos/${FAKE_REPO}/releases?per_page=100") cat "${FAKE_DIR}/releases.json" ;;
  "POST repos/${FAKE_REPO}/releases") echo "${FAKE_ID}" ;;
  "GET repos/${FAKE_REPO}/releases/${FAKE_ID}")
    [[ "$(count get)" -gt "${FAKE_GET_FAILS:-0}" ]] || exit 1
    echo "${FAKE_TAG} true" ;;
  "POST https://uploads.github.com/repos/${FAKE_REPO}/releases/${FAKE_ID}/assets?name=draft-probe.txt")
    [[ "$(count probe)" -gt "${FAKE_PROBE_FAILS:-0}" ]] || exit 1
    echo 777 ;;
  "DELETE repos/${FAKE_REPO}/releases/assets/777") exit "${FAKE_DELETE_RC:-0}" ;;
  "GET repos/${FAKE_REPO}/releases/${FAKE_ID}/assets?per_page=100") printf '%s' "${FAKE_LEFT:-}" ;;
  *) echo "fake gh: unexpected ${method} ${endpoint}" >&2; exit 64 ;;
esac
`
	fakeDraftSleep = "#!/usr/bin/env bash\nexit 0\n"
)

var draftTools = []string{"bash", "jq", "mktemp", "rm", "paste", "cat", "seq"}

type draftRun struct {
	tag, commit, name string
	releases          string // the listing the API answers; "[]" when empty
	getFails          string
	probeFails        string
	deleteRC          string
	left              string
}

const (
	draftRepo   = "example/control"
	draftID     = "431673868"
	draftCommit = "0abe60b1b2d74249c1b1edb12187ede30c14695b"
)

// runDraft runs the script with the fakes and returns its output, its exit
// status and the calls gh received.
func runDraft(t *testing.T, run draftRun) (string, int, []string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tool := range draftTools {
		if err := os.Symlink(settingsTool(t, tool), filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{"gh": fakeDraftGh, "sleep": fakeDraftSleep} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o700); err != nil { //nolint:gosec // G306: the fake has to be executable
			t.Fatal(err)
		}
	}
	releases := run.releases
	if releases == "" {
		releases = "[]"
	}
	if err := os.WriteFile(filepath.Join(dir, "releases.json"), []byte(releases), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(settingsTool(t, "bash"), filepath.Join(repoRoot(t), "scripts", "draft-release.sh"), run.tag, run.commit, run.name) //nolint:gosec // G204: bash from PATH running this repository's own script
	cmd.Env = []string{
		"PATH=" + bin, "HOME=" + dir, "TMPDIR=" + dir, "GITHUB_REPOSITORY=" + draftRepo,
		"FAKE_DIR=" + dir, "FAKE_REPO=" + draftRepo, "FAKE_ID=" + draftID, "FAKE_TAG=" + run.tag,
		"FAKE_GET_FAILS=" + run.getFails, "FAKE_PROBE_FAILS=" + run.probeFails,
		"FAKE_DELETE_RC=" + run.deleteRC, "FAKE_LEFT=" + run.left,
	}
	out, err := fileOutput(t, cmd, true)
	return out, exitCode(t, err), fakeCalls(t, filepath.Join(dir, "gh"))
}

func countCalls(calls []string, want string) int {
	n := 0
	for _, c := range calls {
		if strings.Contains(c, want) {
			n++
		}
	}
	return n
}

func TestDraftReleaseMakesOneDraftAndProvesBothHosts(t *testing.T) {
	fresh := draftRun{tag: "v0.16.0", commit: draftCommit, name: "Example 0.16.0"}
	out, code, calls := runDraft(t, fresh)
	if code != 0 || strings.TrimSpace(out) != draftID {
		t.Fatalf("exit %d, output %q, calls %q", code, out, calls)
	}
	want := []string{
		"api repos/" + draftRepo + "/releases?per_page=100 --paginate",
		"api --method POST repos/" + draftRepo + "/releases -f tag_name=v0.16.0 -f target_commitish=" + draftCommit + " -f name=Example 0.16.0 -F draft=true -F prerelease=false --jq .id",
		"api repos/" + draftRepo + "/releases/" + draftID + ` --jq "\(.tag_name) \(.draft)"`,
		"api --method POST https://uploads.github.com/repos/" + draftRepo + "/releases/" + draftID + "/assets?name=draft-probe.txt -H Content-Type: text/plain --input ",
		"api --method DELETE repos/" + draftRepo + "/releases/assets/777",
		"api repos/" + draftRepo + "/releases/" + draftID + "/assets?per_page=100 --paginate --jq .[].name",
	}
	if len(calls) != len(want) {
		t.Fatalf("gh got %d calls, want %d: %q", len(calls), len(want), calls)
	}
	for i, w := range want {
		if !strings.HasPrefix(calls[i], w) {
			t.Errorf("call %d is %q, want a prefix of %q", i, calls[i], w)
		}
	}
	t.Run("a pre-release tag is drafted as one", func(t *testing.T) {
		pre := fresh
		pre.tag = "v0.16.0-rc.1"
		_, code, calls := runDraft(t, pre)
		if code != 0 || countCalls(calls, "-F prerelease=true") != 1 {
			t.Fatalf("exit %d, calls %q", code, calls)
		}
	})
	t.Run("a host that serves the draft late is waited for", func(t *testing.T) {
		late := fresh
		late.getFails, late.probeFails = "3", "2"
		_, code, calls := runDraft(t, late)
		if code != 0 || countCalls(calls, "/releases/"+draftID+" --jq") != 4 || countCalls(calls, "draft-probe.txt") != 3 {
			t.Fatalf("exit %d, calls %q", code, calls)
		}
	})
}

func TestDraftReleaseRefusesWhatItCannotProve(t *testing.T) {
	fresh := draftRun{tag: "v0.16.0", commit: draftCommit, name: "Example 0.16.0"}
	for name, c := range map[string]struct {
		change func(*draftRun)
		made   bool // whether a draft was made before the refusal
		never  string
	}{
		"a draft for the tag exists":        {change: func(r *draftRun) { r.releases = `[{"id": 5, "tag_name": "v0.16.0", "draft": true}]` }, never: "--method POST"},
		"a published release for the tag":   {change: func(r *draftRun) { r.releases = `[{"id": 5, "tag_name": "v0.16.0", "draft": false}]` }, never: "--method POST"},
		"the API never serves the draft":    {change: func(r *draftRun) { r.getFails = "99" }, made: true, never: "draft-probe.txt"},
		"the upload host never serves it":   {change: func(r *draftRun) { r.probeFails = "99" }, made: true, never: "--method DELETE"},
		"the probe cannot be removed":       {change: func(r *draftRun) { r.deleteRC = "1" }, made: true, never: "assets?per_page"},
		"an asset is left on the draft":     {change: func(r *draftRun) { r.left = "draft-probe.txt" }, made: true},
		"not a release tag":                 {change: func(r *draftRun) { r.tag = "v1.2" }, never: "api"},
		"not a commit":                      {change: func(r *draftRun) { r.commit = "0abe60b1" }, never: "api"},
		"an empty name":                     {change: func(r *draftRun) { r.name = "" }, never: "api"},
		"a release of another tag is no bar": {change: func(r *draftRun) { r.releases = `[{"id": 5, "tag_name": "v0.15.0", "draft": false}]` }, made: true},
	} {
		t.Run(name, func(t *testing.T) {
			run := fresh
			c.change(&run)
			out, code, calls := runDraft(t, run)
			if name == "a release of another tag is no bar" {
				if code != 0 {
					t.Fatalf("exit %d: %s", code, out)
				}
				return
			}
			if code == 0 {
				t.Fatalf("passed: %s\ncalls %q", out, calls)
			}
			if !strings.Contains(out, "draft-release:") {
				t.Fatalf("the refusal does not name the script: %s", out)
			}
			if made := countCalls(calls, "--method POST repos/") == 1; made != c.made {
				t.Errorf("a draft made: %v, want %v; calls %q", made, c.made, calls)
			}
			if c.never != "" && countCalls(calls, c.never) != 0 {
				t.Errorf("gh was still asked %q: %q", c.never, calls)
			}
			if countCalls(calls, "/releases/"+draftID+" --jq") > 12 || countCalls(calls, "draft-probe.txt") > 12 {
				t.Errorf("a host was asked more than 12 times: %q", calls)
			}
		})
	}
}
