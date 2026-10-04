package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/guardana/control/internal/gatewayconfig"
)

// TestDoctorNamesACommandAndNotItsArguments: an argument can carry a
// credential, so doctor prints a command upstream's program and how many
// arguments it takes, never their text.
func TestDoctorNamesACommandAndNotItsArguments(t *testing.T) {
	var out bytes.Buffer
	printUpstream(&out, 0, gatewayconfig.UpstreamConfig{Name: "files", Command: "/usr/bin/server", Args: []string{"--api-token=SECRETMARKER", "--root"}})
	line := out.String()
	if strings.Contains(line, "SECRETMARKER") || strings.Contains(line, "--root") {
		t.Errorf("doctor printed an argument: %q", line)
	}
	if !strings.Contains(line, "command /usr/bin/server (2 arguments)") {
		t.Errorf("doctor's line %q does not name the command and its argument count", line)
	}
}
