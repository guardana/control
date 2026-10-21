package docscheck

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckShellReadsAScriptNamedWithoutSh: a script without the .sh suffix is
// checked under every first line that names a shell shellcheck reads, and a
// file that names another interpreter, or none, is left alone.
func TestCheckShellReadsAScriptNamedWithoutSh(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Fatalf("shellcheck is not on PATH, so check-shell cannot be held to anything: %v", err)
	}
	dirty := "\necho $UNQUOTED > $1\n"
	for _, first := range []string{
		"#!/bin/bash", "#!/bin/sh", "#!/usr/bin/env bash", "#!/bin/bash -eu", "#!/usr/bin/bash",
		"#!/usr/local/bin/bash", "#!/usr/bin/env -S bash -e", "#! /bin/bash", "#!/bin/dash", "#!/bin/ksh",
		"#!/bin/bash\r",
	} {
		out, err := runCheckShell(t, first+dirty)
		if err == nil || !strings.Contains(out, "tools/run") || !strings.Contains(out, "SC2086") {
			t.Errorf("%q: check-shell = %v, want SC2086 in tools/run:\n%s", first, err, out)
		}
	}
	for _, first := range []string{"#!/usr/bin/env python3", "#!/bin/bashful", "no first line of a script"} {
		if out, err := runCheckShell(t, first+dirty); err != nil {
			t.Errorf("%q: check-shell = %v, want it left alone:\n%s", first, err, out)
		}
	}
}

// runCheckShell runs check-shell over a tree with no .git that holds the
// script, its library, one workflow and tools/run with the given content.
func runCheckShell(t *testing.T, content string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ from, to string }{
		{"scripts/check-shell.sh", "scripts/check-shell.sh"},
		{"scripts/lib/repo-files.sh", "scripts/lib/repo-files.sh"},
		{".github/workflows/pull-request.yml", ".github/workflows/pull-request.yml"},
	} {
		data, err := os.ReadFile(filepath.Join(root, f.from)) //nolint:gosec // G304: a file of this repository the test names
		if err != nil {
			t.Fatal(err)
		}
		place(t, dir, f.to, data)
	}
	place(t, dir, "tools/run", []byte(content))
	cmd := exec.Command("/bin/bash", filepath.Join(dir, "scripts", "check-shell.sh")) //nolint:gosec // G204: the script this test staged
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	return out.String(), err
}

func place(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil { //nolint:gosec // G703: a path under this test's own temporary directory
		t.Fatal(err)
	}
}
