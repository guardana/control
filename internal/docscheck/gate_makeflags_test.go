package docscheck

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestTheGateRefusesALoosenedMake: make quality asked for a dry run or a
// question, on the command line or through the environment as an earlier CI
// step could set it, and make quality under GOFLAGS, another makefile or a
// shell startup file, each refuse before any recipe, naming why. Every form
// here runs no recipe even without the guard, so the test cannot start the
// gate.
func TestTheGateRefusesALoosenedMake(t *testing.T) {
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Fatalf("the gate needs make: %v", err)
	}
	root := repoRoot(t)
	empty := t.TempDir() + "/empty.mk"
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var base []string
	for _, kv := range os.Environ() {
		switch name, _, _ := strings.Cut(kv, "="); name {
		case "MAKEFLAGS", "MFLAGS", "MAKELEVEL", "GNUMAKEFLAGS", "MAKEFILES", "GOFLAGS", "BASH_ENV", "ENV":
		default:
			base = append(base, kv)
		}
	}
	for _, c := range []struct {
		name string
		args []string
		env  []string
		want string
	}{
		{"a dry run on the command line", []string{"-n", "quality"}, nil, "MAKEFLAGS asks for a dry run"},
		{"a question on the command line", []string{"-q", "quality"}, nil, "MAKEFLAGS asks for a dry run"},
		{"a dry run through the environment", []string{"quality"}, []string{"MAKEFLAGS=n"}, "MAKEFLAGS asks for a dry run"},
		{"GOFLAGS through the environment", []string{"-n", "quality"}, []string{"GOFLAGS=-tags=gate_off"}, "GOFLAGS is set"},
		{"a shell startup file", []string{"-n", "quality"}, []string{"BASH_ENV=" + empty}, "BASH_ENV or ENV is set"},
		{"another makefile", []string{"-n", "quality"}, []string{"MAKEFILES=" + empty}, "BASH_ENV or ENV is set"},
	} {
		t.Run(c.name, func(t *testing.T) {
			cmd := exec.Command(makeBin, append([]string{"-C", root}, c.args...)...) //nolint:gosec // G204: make, at the repository root, with fixed arguments
			cmd.Env = append(append([]string(nil), base...), c.env...)
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), c.want) {
				t.Errorf("make %s with %v = %v, want a refusal saying %q:\n%.600s", strings.Join(c.args, " "), c.env, err, c.want, out)
			}
		})
	}
	cmd := exec.Command(makeBin, "-C", root, "-n", "quality-quick") //nolint:gosec // G204: make, at the repository root, with fixed arguments
	cmd.Env = base
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("make -n quality-quick, which the guard leaves alone, = %v:\n%.600s", err, out)
	}
}
