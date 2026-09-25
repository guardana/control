package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/guardana/control/internal/brand"
)

// maxSiblingOutput bounds what one run of a sibling binary may write to each
// stream before the rest is dropped.
const maxSiblingOutput = 64 << 10

// installBoth is what every refusal of a sibling says to do about it.
const installBoth = "install both binaries from one tree, for instance with go install ./cmd/..."

// sibling is another binary of this product that this one runs: found beside
// this executable or named by the operator, never looked up on the search
// path, and of this binary's own version.
type sibling struct {
	path    string
	timeout time.Duration
}

// findSibling returns the binary called name, at named when that is set and
// beside this executable otherwise, once it has answered with this binary's
// version. The search path is never read: a binary found there could be
// anyone's.
func findSibling(ctx context.Context, name, named string, timeout time.Duration) (sibling, error) {
	path := named
	if path == "" {
		self, err := os.Executable()
		if err != nil {
			return sibling{}, fmt.Errorf("cannot locate this executable to find %s beside it: %w", name, err)
		}
		path = filepath.Join(filepath.Dir(self), name+executableSuffix())
	}
	path, err := absolute(path)
	if err != nil {
		return sibling{}, fmt.Errorf("no %s at %s: %w; %s", name, named, err, installBoth)
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		return sibling{}, fmt.Errorf("no %s at %s: %w; %s", name, path, err, installBoth)
	case !info.Mode().IsRegular():
		return sibling{}, fmt.Errorf("%s is not a regular file; %s", path, installBoth)
	}
	s := sibling{path: path, timeout: timeout}
	out, err := s.run(ctx)
	if err != nil {
		return sibling{}, fmt.Errorf("asking %s for its version: %w", path, err)
	}
	first, _, _ := strings.Cut(out, "\n")
	theirs, ok := strings.CutPrefix(first, brand.Name+" ")
	if !ok || theirs != version {
		return sibling{}, fmt.Errorf("%s answers %q, and this binary is %s %s; %s",
			path, first, brand.Name, version, installBoth)
	}
	return s, nil
}

// absolute is path under the working directory when it is relative, and
// otherwise path itself. exec looks a bare name up on the search path, so the
// file stat'd and the one run must both be named absolutely; and the name is
// never cleaned, because ".." after a link is the parent of the link's target
// to the kernel and the link's own directory to a lexical clean.
func absolute(path string) (string, error) {
	if filepath.IsAbs(path) {
		return path, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return wd + string(filepath.Separator) + path, nil
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// run runs the sibling with args, within its bound, and returns what it wrote
// to standard output. An exit other than zero is an error carrying the first
// line it wrote to standard error. A ctx that ends before or during the run
// is reported as itself, never as the bound.
func (s sibling) run(ctx context.Context, args ...string) (string, error) {
	name := filepath.Base(s.path)
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("%s was not run: %w", name, err)
	}
	bounded, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	cmd := exec.CommandContext(bounded, s.path, args...) //nolint:gosec // G204: a binary found beside this one or named by the operator, run without a shell
	var stdout, stderr capped
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// A child the sibling started could hold its output open past the kill.
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	switch {
	case ctx.Err() != nil:
		return "", fmt.Errorf("%s was stopped: %w", name, ctx.Err())
	case bounded.Err() != nil:
		return "", fmt.Errorf("%s did not finish within %v", name, s.timeout)
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			line, _, _ := strings.Cut(strings.TrimSpace(stderr.b.String()), "\n")
			return "", fmt.Errorf("%s exited %d: %s", name, exit.ExitCode(), line)
		}
		return "", err
	}
	return stdout.b.String(), nil
}

// capped keeps at most maxSiblingOutput bytes and takes the rest without
// keeping it, so a sibling that writes too much is never blocked on a pipe.
type capped struct{ b bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxSiblingOutput - c.b.Len(); room > 0 {
		c.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
