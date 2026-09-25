//go:build unix

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "control")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil { //nolint:gosec // G306: a script this test runs
		t.Fatalf("writing the script: %v", err)
	}
	return path
}

// TestASiblingAnswersWithThisBinarysVersion: a sibling is taken only when the
// first line it prints without arguments names this binary's version, and
// never found anywhere but where it was named or beside this executable.
func TestASiblingAnswersWithThisBinarysVersion(t *testing.T) {
	ctx := context.Background()
	same := script(t, `echo "`+brand.Name+` `+version+`"; echo "a status line"`)
	if s, err := findSibling(ctx, brand.CLI, same, 10*time.Second); err != nil || s.path != same {
		t.Fatalf("a sibling of this version: %+v, %v", s, err)
	}
	for _, c := range []struct {
		name, path, says string
	}{
		{"another version", script(t, `echo "`+brand.Name+` `+version+`.1"`), `answers "` + brand.Name + " " + version + `.1"`},
		{"another product", script(t, `echo "other `+version+`"`), "go install ./cmd/..."},
		{"no version at all", script(t, `true`), `answers ""`},
		{"a failing binary", script(t, `echo "it broke" >&2; exit 3`), "exited 3: it broke"},
		{"a directory", t.TempDir(), "not a regular file"},
		{"nothing there", filepath.Join(t.TempDir(), "absent"), "go install ./cmd/..."},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := findSibling(ctx, brand.CLI, c.path, 10*time.Second); err == nil || !strings.Contains(err.Error(), c.says) {
				t.Errorf("err = %v, want one saying %q", err, c.says)
			}
		})
	}
	// Beside this test binary there is no such command, and the search path
	// is not read even when it holds one.
	t.Setenv("PATH", filepath.Dir(same)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if _, err := findSibling(ctx, filepath.Base(same), "", 10*time.Second); err == nil || !strings.Contains(err.Error(), "go install ./cmd/...") {
		t.Errorf("a sibling found on the search path: err = %v", err)
	}
}

// TestASiblingIsBounded: a run past its bound is an error, and what a sibling
// writes is kept to the bound without blocking it.
func TestASiblingIsBounded(t *testing.T) {
	slow := sibling{path: script(t, `sleep 5`), timeout: 100 * time.Millisecond}
	if _, err := slow.run(context.Background()); err == nil || !strings.Contains(err.Error(), "did not finish within 100ms") {
		t.Errorf("a slow sibling: err = %v", err)
	}
	loud := sibling{path: script(t, `head -c 200000 /dev/zero`), timeout: 5 * time.Second}
	out, err := loud.run(context.Background())
	if err != nil || len(out) != maxSiblingOutput {
		t.Errorf("a loud sibling: %d bytes, %v; want %d", len(out), err, maxSiblingOutput)
	}
}

// TestABareControlNameRunsTheFileItNames: a --control given as a bare name is
// the file of that name in the working directory, checked and run, and never
// the one of that name on the search path.
func TestABareControlNameRunsTheFileItNames(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	cwd, onPath := t.TempDir(), t.TempDir()
	for dir, who := range map[string]string{cwd: "cwd", onPath: "search-path"} {
		body := "#!/bin/sh\necho \"" + brand.Name + " " + version + "\"\necho " + who + " \"$@\" >> " + marker + "\n"
		if err := os.WriteFile(filepath.Join(dir, brand.CLI), []byte(body), 0o700); err != nil { //nolint:gosec // G306: a script this test runs
			t.Fatal(err)
		}
	}
	t.Chdir(cwd)
	t.Setenv("PATH", onPath+string(os.PathListSeparator)+os.Getenv("PATH"))
	s, err := findSibling(context.Background(), brand.CLI, brand.CLI, 5*time.Second)
	if err != nil {
		t.Fatalf("findSibling: %v", err)
	}
	if _, err := s.run(context.Background(), "pause", "list", "/x"); err != nil {
		t.Fatalf("run: %v", err)
	}
	ran, err := os.ReadFile(marker) //nolint:gosec // G304: a file under the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	if want := "cwd\ncwd pause list /x\n"; !filepath.IsAbs(s.path) || string(ran) != want {
		t.Errorf("path %q ran %q, want an absolute path and %q", s.path, ran, want)
	}
}

// TestASiblingReportsAnInterruptAsItself: a context ended before or during a
// run is the error, and never read as the run's own bound.
func TestASiblingReportsAnInterruptAsItself(t *testing.T) {
	slow := sibling{path: script(t, `sleep 5`), timeout: 10 * time.Second}
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := slow.run(ended); !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "was not run") {
		t.Errorf("an ended context: err = %v", err)
	}
	during, cancelDuring := context.WithCancel(context.Background())
	defer cancelDuring()
	time.AfterFunc(100*time.Millisecond, cancelDuring)
	if _, err := slow.run(during); !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "within") {
		t.Errorf("an interrupt during the run: err = %v", err)
	}
}

// TestAControlPathIsResolvedByTheKernel: a --control naming a link and then
// ".." is the file the kernel resolves, the parent of the link's target, and
// never the one lexical cleaning of the name finds, whether the name is
// relative or absolute.
func TestAControlPathIsResolvedByTheKernel(t *testing.T) {
	work, log := linkedControls(t)
	t.Chdir(work)
	for _, named := range []string{"link/../" + brand.CLI, work + "/link/../" + brand.CLI} {
		t.Run(named, func(t *testing.T) {
			if err := os.Remove(log); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			s, err := findSibling(context.Background(), brand.CLI, named, 5*time.Second)
			if err != nil {
				t.Fatalf("findSibling: %v", err)
			}
			if _, err := s.run(context.Background(), "pause", "list", "/x"); err != nil {
				t.Fatalf("run: %v", err)
			}
			ran, err := os.ReadFile(log) //nolint:gosec // G304: a file under the test's own directory
			if err != nil {
				t.Fatal(err)
			}
			if !filepath.IsAbs(s.path) || string(ran) != "named\nnamed\n" {
				t.Errorf("path %q ran %q, want an absolute path and the file the kernel resolves %q to, twice", s.path, ran, named)
			}
		})
	}
}

// linkedControls lays out two approver's binaries of this version, each
// appending its name to log when run: "named" in target, and "cleaned" in
// work, which also holds link, a link to target/sub.
func linkedControls(t *testing.T) (work, log string) {
	t.Helper()
	root := t.TempDir()
	target, sub := filepath.Join(root, "target"), filepath.Join(root, "target", "sub")
	work, log = filepath.Join(root, "work"), filepath.Join(root, "log")
	for _, d := range []string{sub, work} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for dir, who := range map[string]string{target: "named", work: "cleaned"} {
		body := "#!/bin/sh\necho " + who + " >> " + log + "\n[ $# -eq 0 ] && echo \"" + brand.Name + " " + version + "\"\ntrue\n"
		if err := os.WriteFile(filepath.Join(dir, brand.CLI), []byte(body), 0o700); err != nil { //nolint:gosec // G306: a script this test runs
			t.Fatal(err)
		}
	}
	if err := os.Symlink(sub, filepath.Join(work, "link")); err != nil {
		t.Fatal(err)
	}
	return work, log
}
