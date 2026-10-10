package docscheck

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fakeGh serves `gh api [--paginate] <endpoint>` from a file named after the
// endpoint, or from its pages <name>.1, <name>.2: all of them, concatenated as
// gh prints them, with --paginate, and the first alone without. It logs every
// call. Any other flag, a write among them, is refused, and so is the endpoint
// FAKE_FAIL names, as GitHub refuses a token it does not trust.
const fakeGh = `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >>"${FAKE_DIR}/calls"
[[ "${1:-}" == api ]] || { echo "fake gh: not an api call: $*" >&2; exit 64; }
shift
endpoint=""
paginate=0
for arg in "$@"; do
  case "${arg}" in
    --paginate) paginate=1 ;;
    -*) echo "fake gh: refusing ${arg}" >&2; exit 64 ;;
    *)
      [[ -z "${endpoint}" ]] || { echo "fake gh: two endpoints" >&2; exit 64; }
      endpoint="${arg}"
      ;;
  esac
done
if [[ "${endpoint}" == "${FAKE_FAIL:-}" ]]; then
  echo "gh: Resource not accessible by integration (HTTP 403)" >&2
  exit 1
fi
file="${FAKE_DIR}/answers/$(printf '%s' "${endpoint}" | tr '/?&=' '____')"
if [[ -f "${file}" ]]; then
  cat "${file}"
elif [[ -f "${file}.1" && "${paginate}" == 1 ]]; then
  cat "${file}".*
elif [[ -f "${file}.1" ]]; then
  cat "${file}.1"
else
  echo "gh: Not Found (HTTP 404)" >&2
  exit 1
fi
`

// The tools the check runs. The run's PATH holds these and the fake gh alone,
// so a gh elsewhere on the machine is never reached.
var settingsTools = []string{"bash", "env", "jq", "dirname", "mktemp", "rm", "head", "tr", "sed", "grep", "cat", "ls", "mkdir"}

type settingsRun struct {
	fail string
	noGh bool
	args []string
}

// runSettingsCheck runs the check against the answers with a PATH of the tools
// it needs, and returns its output, its exit status and the fake gh's calls.
func runSettingsCheck(t *testing.T, a answers, run settingsRun) (string, int, []string) {
	t.Helper()
	dir := t.TempDir()
	bin := settingsFakes(t, dir, a, run)
	cmd := exec.Command(settingsTool(t, "bash"), slices.Concat([]string{filepath.Join(repoRoot(t), "scripts", "github-settings-check.sh")}, run.args)...) //nolint:gosec // G204: bash from PATH running this repository's own script
	cmd.Env = append(slices.Clone(settingsEnv), "PATH="+bin, "FAKE_DIR="+dir, "FAKE_FAIL="+run.fail, "HOME="+dir, "TMPDIR="+dir)
	out, err := fileOutput(t, cmd, true)
	code := exitCode(t, err)
	calls, readErr := os.ReadFile(filepath.Join(dir, "calls")) //nolint:gosec // G304: the test's own temporary directory
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
	var lines []string
	if len(calls) > 0 {
		lines = strings.Split(strings.TrimSuffix(string(calls), "\n"), "\n")
	}
	return out, code, lines
}

// fileOutput runs cmd with its standard output, and with combined its
// standard error too, written to a file rather than a pipe: bash 3.2 fails a
// builtin's write to a pipe that a child's exit interrupts, which a loaded
// machine makes likely.
func fileOutput(t *testing.T, cmd *exec.Cmd, combined bool) (string, error) {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "output")
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = f
	if combined {
		cmd.Stderr = f
	}
	runErr := cmd.Run()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(out), runErr
}

// settingsFakes writes the answers where the fake gh serves them from, and a
// bin directory of the tools the check runs, the fake gh among them unless the
// run has none. It returns the bin directory.
func settingsFakes(t *testing.T, dir string, a answers, run settingsRun) string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	answerDir := filepath.Join(dir, "answers")
	for _, d := range []string{bin, answerDir} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range settingsTools {
		if err := os.Symlink(settingsTool(t, tool), filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	if !run.noGh {
		if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(fakeGh), 0o700); err != nil { //nolint:gosec // G306: the fake has to be executable
			t.Fatal(err)
		}
	}
	name := strings.NewReplacer("/", "_", "?", "_", "&", "_", "=", "_")
	for endpoint, body := range a {
		file := filepath.Join(answerDir, name.Replace(endpoint))
		if p, paged := body.(pages); paged {
			for i, page := range p {
				writeAnswer(t, file+"."+strconv.Itoa(i+1), page)
			}
			continue
		}
		writeAnswer(t, file, body)
	}
	return bin
}

func writeAnswer(t *testing.T, file string, body any) {
	t.Helper()
	text, raw := body.(rawAnswer)
	if !raw {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		text = rawAnswer(encoded)
	}
	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}
