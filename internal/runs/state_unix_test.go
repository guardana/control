//go:build unix

package runs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/runs"
)

const (
	public       = controlv1.Sensitivity_SENSITIVITY_PUBLIC
	confidential = controlv1.Sensitivity_SENSITIVITY_CONFIDENTIAL
)

func taint(s runs.State) runs.State { s.Untrusted = true; return s }

func readAtLeast(level controlv1.Sensitivity) func(runs.State) runs.State {
	return func(s runs.State) runs.State {
		if s.MaxRead < level {
			s.MaxRead = level
		}
		return s
	}
}

// setKey replaces one key's value in the state file at path.
func setKey(t *testing.T, path, key, value string) {
	t.Helper()
	rewrite(t, filepath.Dir(path), filepath.Base(path), func(m map[string]json.RawMessage) { m[key] = json.RawMessage(value) })
}

func TestAStateFileThatIsMissingOrSpoiledIsAnError(t *testing.T) {
	cases := map[string]func(t *testing.T, path string){
		"deleted": func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
		"garbled": func(t *testing.T, path string) { writeFile(t, path, []byte(`{"schema_version":"1.0","root":`)) },
		"empty":   func(t *testing.T, path string) { writeFile(t, path, nil) },
		"absent key": func(t *testing.T, path string) {
			rewrite(t, filepath.Dir(path), filepath.Base(path), func(m map[string]json.RawMessage) { delete(m, "max_read") })
		},
		"unknown key":    func(t *testing.T, path string) { setKey(t, path, "taken", `[]`) },
		"unknown level":  func(t *testing.T, path string) { setKey(t, path, "max_read", `"TOP"`) },
		"prefixed level": func(t *testing.T, path string) { setKey(t, path, "max_read", `"SENSITIVITY_PUBLIC"`) },
		"another root": func(t *testing.T, path string) {
			setKey(t, path, "root", `"run-11111111111111111111111111111111"`)
		},
		"another major":  func(t *testing.T, path string) { setKey(t, path, "schema_version", `"2.0"`) },
		"string boolean": func(t *testing.T, path string) { setKey(t, path, "untrusted", `"false"`) },
		"symlinked": func(t *testing.T, path string) {
			elsewhere := filepath.Join(t.TempDir(), "state.json")
			if err := os.Rename(path, elsewhere); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(elsewhere, path); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			dir, a, p := setup(t)
			rec, _ := openRoot(t, a)
			path := filepath.Join(dir, rec.ID+".state.json")
			spoil(t, path)
			if s, err := p.State(t.Context(), rec.ID); err == nil {
				t.Fatalf("State read %+v", s)
			}
			if err := p.Raise(t.Context(), rec.ID, taint); err == nil {
				t.Fatalf("Raise over a spoiled state succeeded")
			}
		})
	}
}

func TestRaiseWithoutTheLockFileFailsAndCreatesNone(t *testing.T) {
	dir, a, p := setup(t)
	rec, _ := openRoot(t, a)
	lock := filepath.Join(dir, rec.ID+".lock")
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	if err := p.Raise(t.Context(), rec.ID, taint); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Raise: %v, want fs.ErrNotExist", err)
	}
	if _, err := os.Lstat(lock); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Raise created the lock file: %v", err)
	}
	if s, err := p.State(t.Context(), rec.ID); err != nil || s.Untrusted {
		t.Fatalf("the state after a failed raise: %+v, %v", s, err)
	}
}

func TestRaiseWritesOnlyWhatJoinChanges(t *testing.T) {
	dir, a, p := setup(t)
	rec, _ := openRoot(t, a)
	path := filepath.Join(dir, rec.ID+".state.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Raise(t.Context(), rec.ID, readAtLeast(public)); err != nil {
		t.Fatal(err)
	}
	if after, err := os.Stat(path); err != nil || !os.SameFile(before, after) {
		t.Fatalf("a join that changed nothing replaced the file: %v", err)
	}
	if err := p.Raise(t.Context(), rec.ID, readAtLeast(confidential)); err != nil {
		t.Fatal(err)
	}
	if after, err := os.Stat(path); err != nil || os.SameFile(before, after) {
		t.Fatalf("a join that raised the state left the file: %v", err)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema_version":"1.0","root":"` + rec.ID + `","untrusted":false,"max_read":"CONFIDENTIAL"}` + "\n"
	if string(raw) != want {
		t.Fatalf("state file\n%s\nwant\n%s", raw, want)
	}
	if _, err := os.Lstat(filepath.Join(dir, rec.ID+".state.tmp")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the temporary file outlived the write: %v", err)
	}
}

func TestRaiseRefusesAStateItCannotWrite(t *testing.T) {
	_, a, p := setup(t)
	rec, _ := openRoot(t, a)
	unnamed := func(s runs.State) runs.State { s.MaxRead = 99; return s }
	if err := p.Raise(t.Context(), rec.ID, unnamed); !errors.Is(err, runs.ErrState) {
		t.Fatalf("Raise to an unnamed level: %v", err)
	}
	if err := p.Raise(t.Context(), rec.ID, nil); !errors.Is(err, runs.ErrState) {
		t.Fatalf("Raise with no join: %v", err)
	}
	if s, err := p.State(t.Context(), rec.ID); err != nil || s.MaxRead != public {
		t.Fatalf("state after refused raises: %+v, %v", s, err)
	}
}

func TestRaiseWaitsForTheLockUntilItsContextEnds(t *testing.T) {
	dir, a, p := setup(t)
	rec, _ := openRoot(t, a)
	held, err := os.OpenFile(filepath.Join(dir, rec.ID+".lock"), os.O_RDWR, 0) //nolint:gosec // G304: the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := p.Raise(ctx, rec.ID, taint); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Raise under a held lock: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Raise(t.Context(), rec.ID, taint); err != nil {
		t.Fatalf("Raise once released: %v", err)
	}
}

// TestTwoPlanesNeverRaiseInsideEachOther holds one plane's join open and
// watches whether the other plane's join runs before it returns. Under the
// root's lock it cannot; without it, it does at once.
func TestTwoPlanesNeverRaiseInsideEachOther(t *testing.T) {
	dir, a, first := setup(t)
	second := openPlane(t, dir)
	rec, _ := openRoot(t, a)
	inside := make(chan struct{})
	entered := make(chan struct{}, 1)
	var overlapped bool
	var wg sync.WaitGroup
	wg.Go(func() {
		err := first.Raise(t.Context(), rec.ID, func(s runs.State) runs.State {
			close(inside)
			select {
			case <-entered:
				overlapped = true
			case <-time.After(200 * time.Millisecond):
			}
			return taint(s)
		})
		if err != nil {
			t.Errorf("first: %v", err)
		}
	})
	<-inside
	err := second.Raise(t.Context(), rec.ID, func(s runs.State) runs.State {
		entered <- struct{}{}
		return readAtLeast(confidential)(s)
	})
	wg.Wait()
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if overlapped {
		t.Fatal("the second plane's join ran while the first held the root")
	}
	s, err := first.State(t.Context(), rec.ID)
	if err != nil || s != (runs.State{Untrusted: true, MaxRead: confidential}) {
		t.Fatalf("final state %+v, %v; want untrusted and CONFIDENTIAL", s, err)
	}
}

// TestPlanesRaisingSideBySideLoseNoRaise has many writers on two planes each
// set one level, and the state must end at the highest and tainted.
func TestPlanesRaisingSideBySideLoseNoRaise(t *testing.T) {
	dir, a, first := setup(t)
	planes := []*runs.Plane{first, openPlane(t, dir)}
	for round := range 5 {
		rec, _ := openRoot(t, a)
		var wg sync.WaitGroup
		joins := []func(runs.State) runs.State{
			taint,
			readAtLeast(controlv1.Sensitivity_SENSITIVITY_INTERNAL),
			readAtLeast(controlv1.Sensitivity_SENSITIVITY_SECRET),
			readAtLeast(confidential),
		}
		for i, join := range joins {
			wg.Go(func() {
				if err := planes[i%2].Raise(t.Context(), rec.ID, join); err != nil {
					t.Errorf("round %d writer %d: %v", round, i, err)
				}
			})
		}
		wg.Wait()
		s, err := first.State(t.Context(), rec.ID)
		if err != nil || s != (runs.State{Untrusted: true, MaxRead: controlv1.Sensitivity_SENSITIVITY_SECRET}) {
			t.Fatalf("round %d: %+v, %v", round, s, err)
		}
	}
}

func TestAChildReadsAndRaisesItsRootsState(t *testing.T) {
	_, a, p := setup(t)
	root, _ := openRoot(t, a)
	_, childToken := openUnder(t, a, root.ID, alice)
	run, err := p.Resolve(t.Context(), childToken, alice, opened)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Raise(t.Context(), run.Root, taint); err != nil {
		t.Fatal(err)
	}
	if s, err := p.State(t.Context(), root.ID); err != nil || !s.Untrusted {
		t.Fatalf("the root after its child raised: %+v, %v", s, err)
	}
	if _, err := p.State(t.Context(), run.ID); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a child's own state: %v, want none", err)
	}
}
