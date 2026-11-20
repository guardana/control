package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// The dispatch test's answers for attempts: bare, it names the one thing to
// do; with a manifest that does not exist it could not run, exit 2.
func init() {
	answers["attempts"] = answer{
		bare: brand.Gateway + " attempts: the one thing to do with attempts is run them: " + brand.Gateway + " attempts " + attemptsForm + "\n",
		refused: func(missing string) []string {
			return []string{"run", "--manifest", missing, "--report", missing + ".report"}
		},
		refusal: brand.Gateway + ": attempts: could not run: ",
		status:  exitCouldNotRun,
	}
}

func TestAttemptsUsage(t *testing.T) {
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"attempts", "run", "--report", "r"}, brand.Gateway + " attempts: --manifest names the attempt manifest to run\n"},
		{[]string{"attempts", "run", "--manifest", "m"}, brand.Gateway + " attempts: --report names the report to write\n"},
		{[]string{"attempts", "run", "--manifest", "m", "--report", "r", "extra"}, brand.Gateway + " attempts: unexpected argument extra\n"},
		{[]string{"attempts", "count"}, brand.Gateway + " attempts: the one thing to do with attempts is run them: " + brand.Gateway + " attempts " + attemptsForm + "\n"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), c.args, &stdout, &stderr); code != exitUsage || stderr.String() != c.want || stdout.Len() != 0 {
			t.Errorf("%q: exit %d, stderr %q; want %d and %q", c.args, code, stderr.String(), exitUsage, c.want)
		}
	}
}

// TestAPlaneModeIsSetOverTheDemos: the mode an attempt's plane names is the
// plane's, whatever the demo's file says, and with none the file's stands.
func TestAPlaneModeIsSetOverTheDemos(t *testing.T) {
	demo := filepath.Join("..", "..", "examples", "vulnerable-mcp-agent", "demo.yaml")
	for _, c := range []struct{ mode, want string }{{"OBSERVE", "OBSERVE"}, {"APPROVE", "APPROVE"}, {"", "ENFORCE"}} {
		cfg, err := demoConfig(devInputs{config: demo, silent: true, mode: c.mode})
		if err != nil {
			t.Fatalf("mode %q: %v", c.mode, err)
		}
		if cfg.ModeName != c.want {
			t.Errorf("mode %q: the plane runs in %s, want %s", c.mode, cfg.ModeName, c.want)
		}
	}
	if _, err := demoConfig(devInputs{config: demo, silent: true, mode: "SOMETIMES"}); err == nil {
		t.Error("a mode the plane does not know was taken")
	}
}
