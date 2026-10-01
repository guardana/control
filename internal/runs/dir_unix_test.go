//go:build unix

package runs_test

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/runs"
)

func TestAForeignNameRefusesTheDirectory(t *testing.T) {
	for _, name := range []string{"notes.txt", "run-abc.run.json", ".hidden", "RUN-00000000000000000000000000000000.lock", "run-00000000000000000000000000000000.state.bak"} {
		t.Run(name, func(t *testing.T) {
			dir, a, _ := setup(t)
			openRoot(t, a)
			writeFile(t, filepath.Join(dir, name), []byte("{}"))
			if _, err := a.List(t.Context(), 10); !errors.Is(err, runs.ErrForeignFile) {
				t.Errorf("List: %v", err)
			}
			if _, err := runs.OpenAdmin(dir); !errors.Is(err, runs.ErrForeignFile) {
				t.Errorf("OpenAdmin: %v", err)
			}
			if _, err := runs.OpenPlane(dir); !errors.Is(err, runs.ErrForeignFile) {
				t.Errorf("OpenPlane: %v", err)
			}
		})
	}
}

func TestAnEntryThatIsNotARegularFileIsForeignWhateverItsName(t *testing.T) {
	dir, a, _ := setup(t)
	rec, _ := openRoot(t, a)
	pipe := filepath.Join(dir, rec.ID+".state.tmp")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.List(t.Context(), 10); !errors.Is(err, runs.ErrForeignFile) {
		t.Fatalf("List over a pipe: %v", err)
	}
	if _, err := runs.OpenPlane(dir); !errors.Is(err, runs.ErrForeignFile) {
		t.Fatalf("OpenPlane over a pipe: %v", err)
	}
}

func TestASymlinkedRecordIsRefused(t *testing.T) {
	dir, a, p := setup(t)
	_, token := openRoot(t, a)
	name := filepath.Join(dir, idOf(t, token)+".run.json")
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.json")
	if err := os.Rename(name, elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, name); err != nil {
		t.Fatal(err)
	}
	_, err := p.Resolve(t.Context(), token, alice, opened)
	if cause := causeOf(t, err); cause != runs.CauseUnreadable || !errors.Is(err, runs.ErrForeignFile) {
		t.Fatalf("Resolve through a link: cause %q, %v", cause, err)
	}
	if _, err := a.List(t.Context(), 10); !errors.Is(err, runs.ErrForeignFile) {
		t.Fatalf("List over a link: %v", err)
	}
}

func TestAGroupWritableDirectoryIsRefused(t *testing.T) {
	dir := newDir(t)
	openAdmin(t, dir)
	if err := os.Chmod(dir, 0o770); err != nil { //nolint:gosec // G302: the mode under test
		t.Fatal(err)
	}
	if _, err := runs.OpenAdmin(dir); !errors.Is(err, runs.ErrPermissions) {
		t.Fatalf("OpenAdmin: %v", err)
	}
	if _, err := runs.OpenPlane(dir); !errors.Is(err, runs.ErrPermissions) {
		t.Fatalf("OpenPlane: %v", err)
	}
}

func TestADirectoryLoosenedAfterOpenRefusesEveryCall(t *testing.T) {
	dir, a, p := setup(t)
	rec, token := openRoot(t, a)
	if err := os.Chmod(dir, 0o702); err != nil { //nolint:gosec // G302: the mode under test
		t.Fatal(err)
	}
	_, err := p.Resolve(t.Context(), token, alice, opened)
	if causeOf(t, err) != runs.CauseUnreadable || !errors.Is(err, runs.ErrDirectoryChanged) {
		t.Errorf("Resolve: %v", err)
	}
	if _, err := p.State(t.Context(), rec.ID); !errors.Is(err, runs.ErrDirectoryChanged) {
		t.Errorf("State: %v", err)
	}
	if err := p.Raise(t.Context(), rec.ID, taint); !errors.Is(err, runs.ErrDirectoryChanged) {
		t.Errorf("Raise: %v", err)
	}
	if _, _, err := a.Open(t.Context(), runs.OpenRequest{Who: alice, TTL: time.Hour, Now: opened}); !errors.Is(err, runs.ErrDirectoryChanged) {
		t.Errorf("Open: %v", err)
	}
	if err := a.CloseRun(t.Context(), rec.ID, opened); !errors.Is(err, runs.ErrDirectoryChanged) {
		t.Errorf("CloseRun: %v", err)
	}
	if _, err := a.List(t.Context(), 10); !errors.Is(err, runs.ErrDirectoryChanged) {
		t.Errorf("List: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory needs its search bit
		t.Fatal(err)
	}
	if _, err := p.Resolve(t.Context(), token, alice, opened); err != nil {
		t.Fatalf("with the mode put back: %v", err)
	}
}

func TestADirectorySwappedAtItsNameIsRefused(t *testing.T) {
	dir, a, p := setup(t)
	_, token := openRoot(t, a)
	if err := os.Rename(dir, dir+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := p.Resolve(t.Context(), token, alice, opened)
	if causeOf(t, err) != runs.CauseUnreadable || !errors.Is(err, runs.ErrDirectoryChanged) {
		t.Fatalf("Resolve: %v", err)
	}
	if _, err := a.List(t.Context(), 10); !errors.Is(err, runs.ErrDirectoryChanged) {
		t.Fatalf("List: %v", err)
	}
}

func TestOnlyTheOperatorMakesARunsDirectory(t *testing.T) {
	dir := newDir(t)
	if _, err := runs.OpenPlane(dir); !errors.Is(err, runs.ErrNotRunsDir) {
		t.Fatalf("OpenPlane of an empty directory: %v", err)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("OpenPlane wrote into the directory: %v, %v", entries, err)
	}
	openAdmin(t, dir)
	openPlane(t, dir)

	other := newDir(t)
	writeFile(t, filepath.Join(other, "run-"+"0123456789abcdef0123456789abcdef"+".lock"), nil)
	if _, err := runs.OpenAdmin(other); !errors.Is(err, runs.ErrNotRunsDir) {
		t.Fatalf("OpenAdmin of a directory with no marker: %v", err)
	}
}

func TestAMarkerThisBuildCannotReadIsRefused(t *testing.T) {
	for name, body := range map[string]string{
		"another kind":   `{"schema_version":"1.0","kind":"approvals"}`,
		"another major":  `{"schema_version":"2.0","kind":"runs"}`,
		"an unknown key": `{"schema_version":"1.0","kind":"runs","x":1}`,
		"an absent key":  `{"kind":"runs"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := newDir(t)
			writeFile(t, filepath.Join(dir, "runs.meta"), []byte(body))
			if _, err := runs.OpenAdmin(dir); !errors.Is(err, runs.ErrNotRunsDir) {
				t.Errorf("OpenAdmin: %v", err)
			}
			if _, err := runs.OpenPlane(dir); !errors.Is(err, runs.ErrNotRunsDir) {
				t.Errorf("OpenPlane: %v", err)
			}
		})
	}
}
