package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestTheStartLineNamesFailOpenRead: policy.fail_open_read is a risk setting,
// so the line run prints once the plane is up says which side it is on.
func TestTheStartLineNamesFailOpenRead(t *testing.T) {
	for _, setting := range []string{"false", "true"} {
		t.Run(setting, func(t *testing.T) {
			tr := newTree(t)
			setEnv(t, "policy.fail_open_read", setting)
			line := startLine(tr.load(t))
			want := ": mode OBSERVE, bundle gateway-fixture, policy.fail_open_read " + setting +
				", evidence in " + filepath.Join(tr.dir, "spool")
			if !strings.HasSuffix(line, want) {
				t.Errorf("the start line is %q, want it to end %q", line, want)
			}
		})
	}
}
