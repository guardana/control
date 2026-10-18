package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// neutralProfile is the bridge's test profile: exit 2 blocks, and its
// events are BeforeTool, AfterTool and AfterToolFailed.
const neutralProfile = "../../internal/hookbridge/testdata/neutral/profile.json"

const (
	admitEvent    = `{"event":"BeforeTool","session":"s-1","call":"c-1","tool":"refund","input":{"amount":5}}`
	completeEvent = `{"event":"AfterTool","session":"s-1","call":"c-1","tool":"refund","input":{"amount":5},"interrupted":false}`
)

// hookRun runs the hook command with event on its standard input and returns
// its status and what it wrote.
func hookRun(t *testing.T, event string, args ...string) (int, string, string) {
	t.Helper()
	saved := hookStdin
	hookStdin = strings.NewReader(event)
	t.Cleanup(func() { hookStdin = saved })
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"hook"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// stateDir is an owner-only directory for the bridge's handles.
func stateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestEveryHookRefusalBlocksAndPrintsNothing: whatever goes wrong, an admit
// exits with the status the runtime blocks on and writes nothing on standard
// output, where a reply could allow, ask, defer or change the call.
func TestEveryHookRefusalBlocksAndPrintsNothing(t *testing.T) {
	nowhere := filepath.Join(t.TempDir(), "no.sock")
	widened := filepath.Join(t.TempDir(), "widened")
	if err := os.Mkdir(widened, 0o755); err != nil { //nolint:gosec // G301: the mode under test
		t.Fatal(err)
	}
	good := func(t *testing.T) []string {
		return []string{"--profile-file", neutralProfile, "--socket", nowhere, "--state-dir", stateDir(t)}
	}
	for _, c := range []struct {
		name  string
		token string
		event string
		args  func(t *testing.T) []string
	}{
		{"an unknown flag", "tok", admitEvent, func(t *testing.T) []string { return append(good(t), "--allow") }},
		{"an argument", "tok", admitEvent, func(t *testing.T) []string { return append(good(t), "extra") }},
		{"both profile flags", "tok", admitEvent, func(t *testing.T) []string {
			return append(good(t), "--profile", "neutral")
		}},
		{"no profile flag", "tok", admitEvent, func(t *testing.T) []string {
			return []string{"--socket", nowhere, "--state-dir", stateDir(t)}
		}},
		{"no socket", "tok", admitEvent, func(t *testing.T) []string {
			return []string{"--profile-file", neutralProfile, "--state-dir", stateDir(t)}
		}},
		{"no state directory", "tok", admitEvent, func(*testing.T) []string {
			return []string{"--profile-file", neutralProfile, "--socket", nowhere}
		}},
		{"no run token", "", admitEvent, good},
		{"a profile this binary does not carry", "tok", admitEvent, func(t *testing.T) []string {
			return []string{"--profile", "no-such-profile", "--socket", nowhere, "--state-dir", stateDir(t)}
		}},
		{"an event that is not JSON", "tok", "not json", good},
		{"an event the profile does not map", "tok", strings.Replace(admitEvent, "BeforeTool", "Elsewhere", 1), good},
		{"a receiver that does not answer", "tok", admitEvent, good},
		{"a state directory others may enter", "tok", admitEvent, func(*testing.T) []string {
			return []string{"--profile-file", neutralProfile, "--socket", nowhere, "--state-dir", widened}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(hookTokenVariable, c.token)
			code, stdout, stderr := hookRun(t, c.event, c.args(t)...)
			if code != 2 || stdout != "" {
				t.Fatalf("exit %d, stdout %q, stderr %q; want 2 and nothing on stdout", code, stdout, stderr)
			}
			if strings.Count(strings.TrimSuffix(stderr, "\n"), "\n") > 0 || stderr == "" {
				t.Errorf("stderr %q, want one line", stderr)
			}
		})
	}
}

// TestACompletionNoAdmitOpenedExitsOne: a completion with no entry sends
// nothing and exits 1, which the runtime shows and does not act on.
func TestACompletionNoAdmitOpenedExitsOne(t *testing.T) {
	t.Setenv(hookTokenVariable, "tok")
	code, stdout, stderr := hookRun(t, completeEvent, "--profile-file", neutralProfile,
		"--socket", filepath.Join(t.TempDir(), "no.sock"), "--state-dir", stateDir(t))
	if code != 1 || stdout != "" || !strings.Contains(stderr, "no admit of this call is open") {
		t.Fatalf("exit %d, stdout %q, stderr %q; want 1, nothing on stdout, and the unmatched completion named", code, stdout, stderr)
	}
}
