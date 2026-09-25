//go:build unix

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// devProcess is one run of the built gateway's dev command.
type devProcess struct {
	cmd            *exec.Cmd
	stdout, stderr *syncBuffer
	exited         chan struct{}
}

func startDev(t *testing.T, args ...string) *devProcess {
	t.Helper()
	gatewayBin, _ := builtBinaries(t)
	cmd := exec.Command(gatewayBin, append([]string{"dev"}, args...)...) //nolint:gosec // G204: the binary this test built
	cmd.Env = environ()
	d := &devProcess{cmd: cmd, stdout: &syncBuffer{}, stderr: &syncBuffer{}, exited: make(chan struct{})}
	cmd.Stdout, cmd.Stderr = d.stdout, d.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting dev: %v", err)
	}
	go func() {
		_ = cmd.Wait()
		close(d.exited)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-d.exited
	})
	return d
}

// ready waits for the last line dev prints once every part is up, and
// returns every `name: value` line it printed.
func (d *devProcess) ready(t *testing.T) map[string]string {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(d.stdout.String(), "\nscenario_command: ") {
		select {
		case <-d.exited:
			t.Fatalf("dev exited before it was ready:\n%s\n%s", d.stdout.String(), d.stderr.String())
		case <-time.After(20 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatalf("dev was not ready within 30s:\n%s\n%s", d.stdout.String(), d.stderr.String())
		}
	}
	values := map[string]string{}
	for _, line := range lines(d.stdout.String()) {
		if name, value, ok := strings.Cut(line, ": "); ok {
			values[name] = value
		}
	}
	return values
}

// exit waits up to limit for dev to exit and returns its status.
func (d *devProcess) exit(t *testing.T, limit time.Duration) int {
	t.Helper()
	select {
	case <-d.exited:
	case <-time.After(limit):
		t.Fatalf("dev did not exit within %v:\n%s\n%s", limit, d.stdout.String(), d.stderr.String())
	}
	return d.cmd.ProcessState.ExitCode()
}

// pagePID is the process id of the page dev started, read from the
// system's process table as the one child of dev running console.
func (d *devProcess) pagePID(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,args=").Output()
	if err != nil {
		t.Fatalf("ps: %v", err)
	}
	var found []int
	for _, line := range lines(string(out)) {
		var pid, ppid int
		if _, err := fmt.Sscan(line, &pid, &ppid); err == nil && ppid == d.cmd.Process.Pid && strings.Contains(line, " console ") {
			found = append(found, pid)
		}
	}
	if len(found) != 1 {
		t.Fatalf("dev has %d console children, want 1:\n%s", len(found), out)
	}
	return found[0]
}

// gone waits up to limit for the process pid to be gone.
func gone(pid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}
