package policystate_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
)

func TestOpenRefusesTheOtherKindBothWays(t *testing.T) {
	for _, tc := range []struct{ made, opened policystate.Kind }{
		{policystate.KindPlane, policystate.KindSigner},
		{policystate.KindSigner, policystate.KindPlane},
	} {
		t.Run(string(tc.made), func(t *testing.T) {
			dir := initDir(t, tc.made)
			if _, err := policystate.Open(dir, tc.opened); !errors.Is(err, policystate.ErrWrongKind) {
				t.Errorf("Open(%s) of a %s directory: %v, want ErrWrongKind", tc.opened, tc.made, err)
			}
			s, err := policystate.Open(dir, tc.made)
			if err != nil {
				t.Fatalf("Open(%s) of its own kind: %v", tc.made, err)
			}
			_ = s.Close()
		})
	}
}

func TestOpenRefusesWhatIsNotAFloorDirectoryAndWritesNothing(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		dir := newDir(t)
		if _, err := policystate.Open(dir, policystate.KindPlane); !errors.Is(err, policystate.ErrNotStateDir) {
			t.Errorf("Open: %v, want ErrNotStateDir", err)
		}
		if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("Open made the directory: %v", err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		dir := newDir(t)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := policystate.Open(dir, policystate.KindPlane); !errors.Is(err, policystate.ErrNotStateDir) {
			t.Errorf("Open: %v, want ErrNotStateDir", err)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("Open left %v, %v in an empty directory", entries, err)
		}
	})
	t.Run("a kind of no meaning", func(t *testing.T) {
		dir := initDir(t, policystate.KindPlane)
		if _, err := policystate.Open(dir, policystate.Kind("runs")); !errors.Is(err, policystate.ErrKind) {
			t.Errorf("Open: %v, want ErrKind", err)
		}
	})
}

func TestAMissingFloorFileIsAnErrorAndNeverAFirstStart(t *testing.T) {
	dir, s := raised(t)
	for name, call := range map[string]func() error{
		"Floor of an id never initialised": func() error { _, err := s.Floor(t.Context(), idB); return err },
		"Raise of an id never initialised": func() error {
			_, err := s.Raise(t.Context(), statement(t, idB, 1, d3, "11:00:00"), noon(t))
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := call()
			if !errors.Is(err, policystate.ErrNoFloor) || !strings.Contains(err.Error(), fileB) {
				t.Errorf("%v, want ErrNoFloor naming %s", err, fileB)
			}
		})
	}
	if err := os.Remove(filepath.Join(dir, fileA)); err != nil {
		t.Fatal(err)
	}
	if f, err := s.Floor(t.Context(), idA); !errors.Is(err, policystate.ErrNoFloor) {
		t.Errorf("Floor of a removed file: %s, %v; want ErrNoFloor", describe(f), err)
	}
	if f, err := s.Raise(t.Context(), statement(t, idA, 1, d3, "11:00:00"), noon(t)); !errors.Is(err, policystate.ErrNoFloor) {
		t.Errorf("Raise of a removed file: %s, %v; want ErrNoFloor", describe(f), err)
	}
	if _, err := os.Lstat(filepath.Join(dir, fileA)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Raise created the removed file: %v", err)
	}
}

func TestRaiseWritesTheRaisedFloorWholeAndReturnsIt(t *testing.T) {
	dir := initDir(t, policystate.KindPlane)
	s := open(t, dir)
	f, err := s.Raise(t.Context(), statement(t, idA, 5, d5, "10:00:00"), noon(t))
	if err != nil {
		t.Fatalf("Raise: %v", err)
	}
	if got := describe(f); got != "bundle-a serial 5 "+d5+" issued 2026-09-11T10:00:00Z latest 2026-09-11T10:00:00Z" {
		t.Errorf("Raise returned %s", got)
	}
	want := `{"schema_version":"1.0","bundle_id":"bundle-a","serial":5,"digest":"` + d5 +
		`","issued_at":"2026-09-11T10:00:00Z","latest_issued_at":"2026-09-11T10:00:00Z","reset_reason":null,"reset_from":null}` + "\n"
	if got := readFile(t, filepath.Join(dir, fileA)); got != want {
		t.Errorf("the file holds\n%s\nwant\n%s", got, want)
	}
	info, err := os.Lstat(filepath.Join(dir, fileA))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the file's mode: %v, %v; want 0600", info, err)
	}
}

func TestRaiseRefusesWhatTheStoredFloorRefusesAndLeavesTheFile(t *testing.T) {
	for name, tc := range map[string]struct {
		st   func(t *testing.T) policy.Statement
		want error
	}{
		"a lower serial":             {func(t *testing.T) policy.Statement { return statement(t, idA, 4, d3, "11:30:00") }, policy.ErrBelowFloor},
		"the serial, another digest": {func(t *testing.T) policy.Statement { return statement(t, idA, 5, d6, "11:30:00") }, policy.ErrFloorSerialReused},
		"an earlier renewal":         {func(t *testing.T) policy.Statement { return statement(t, idA, 5, d5, "10:30:00") }, policy.ErrBelowFloor},
		"dated after the clock":      {func(t *testing.T) policy.Statement { return statement(t, idA, 6, d6, "12:00:01") }, policy.ErrStatementFuture},
	} {
		t.Run(name, func(t *testing.T) {
			dir, s := raised(t)
			before := readFile(t, filepath.Join(dir, fileA))
			if f, err := s.Raise(t.Context(), tc.st(t), noon(t)); !errors.Is(err, tc.want) {
				t.Errorf("Raise: %s, %v; want %v", describe(f), err, tc.want)
			}
			if after := readFile(t, filepath.Join(dir, fileA)); after != before {
				t.Errorf("a refused raise changed the file to %s", after)
			}
		})
	}
}

func TestRaiseComparesAgainstTheFileAndNotItsMemory(t *testing.T) {
	dir := initDir(t, policystate.KindPlane)
	first, second := open(t, dir), open(t, dir)
	if _, err := first.Raise(t.Context(), statement(t, idA, 5, d5, "10:00:00"), noon(t)); err != nil {
		t.Fatalf("first raise: %v", err)
	}
	if f, err := first.Floor(t.Context(), idA); err != nil || f.Serial() != 5 {
		t.Fatalf("Floor: %s, %v", describe(f), err)
	}
	if _, err := second.Raise(t.Context(), statement(t, idA, 7, d7, "11:00:00"), noon(t)); err != nil {
		t.Fatalf("the other handle's raise: %v", err)
	}
	if f, err := first.Raise(t.Context(), statement(t, idA, 6, d6, "11:30:00"), noon(t)); !errors.Is(err, policy.ErrBelowFloor) {
		t.Errorf("a raise to 6 past a floor another handle raised to 7: %s, %v; want ErrBelowFloor", describe(f), err)
	}
	if f, err := first.Floor(t.Context(), idA); err != nil || f.Serial() != 7 {
		t.Errorf("Floor: %s, %v; want serial 7", describe(f), err)
	}
}

func TestRaiseWritesOnlyWhatChangesAndReplacesTheFileWhole(t *testing.T) {
	dir, s := raised(t)
	path := filepath.Join(dir, fileA)
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := s.Raise(t.Context(), statement(t, idA, 5, d5, "11:00:00"), noon(t))
	if err != nil || f.Serial() != 5 {
		t.Fatalf("Raise of the floor's own statement: %s, %v", describe(f), err)
	}
	if same, err := os.Lstat(path); err != nil || !os.SameFile(before, same) {
		t.Errorf("the floor's own statement rewrote the file: %v", err)
	}
	if _, err := s.Raise(t.Context(), statement(t, idA, 5, d5, "11:30:00"), noon(t)); err != nil {
		t.Fatalf("a renewal: %v", err)
	}
	if after, err := os.Lstat(path); err != nil || os.SameFile(before, after) {
		t.Errorf("a renewal wrote the file in place rather than replacing it: %v", err)
	}
}

func TestAClosedOrUnopenedStoreFailsClosed(t *testing.T) {
	dir := initDir(t, policystate.KindPlane)
	closed := open(t, dir)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*policystate.Store{"nil": nil, "zero": {}, "closed": closed} {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Floor(t.Context(), idA); !errors.Is(err, policystate.ErrClosed) {
				t.Errorf("Floor: %v", err)
			}
			if _, err := s.Record(t.Context(), idA); !errors.Is(err, policystate.ErrClosed) {
				t.Errorf("Record: %v", err)
			}
			if _, err := s.Raise(t.Context(), statement(t, idA, 1, d3, "10:00:00"), noon(t)); !errors.Is(err, policystate.ErrClosed) {
				t.Errorf("Raise: %v", err)
			}
			if err := s.Close(); !errors.Is(err, policystate.ErrClosed) {
				t.Errorf("Close: %v", err)
			}
		})
	}
	if got := readFile(t, filepath.Join(dir, fileA)); got != emptyBodyA {
		t.Errorf("a closed store wrote %s", got)
	}
}

func TestACanceledContextReadsAndWritesNothing(t *testing.T) {
	dir, s := raised(t)
	before := readFile(t, filepath.Join(dir, fileA))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if f, err := s.Floor(ctx, idA); !errors.Is(err, context.Canceled) {
		t.Errorf("Floor under a canceled context: %s, %v", describe(f), err)
	}
	if f, err := s.Raise(ctx, statement(t, idA, 6, d6, "11:30:00"), noon(t)); !errors.Is(err, context.Canceled) {
		t.Errorf("Raise under a canceled context: %s, %v", describe(f), err)
	}
	if after := readFile(t, filepath.Join(dir, fileA)); after != before {
		t.Errorf("a canceled raise wrote %s", after)
	}
}

// errCrash stands for a process that died inside a write.
var errCrash = errors.New("the process died here")

func TestACrashInsideAReplaceLeavesTheOldFloorOrTheNew(t *testing.T) {
	t.Run("before the rename", func(t *testing.T) {
		dir, s := raised(t)
		before := readFile(t, filepath.Join(dir, fileA))
		restore := policystate.SetReplace(func(root *os.Root, _ string, body []byte, _ fs.FileMode) error {
			torn := body[:len(body)/2]
			return errors.Join(root.WriteFile(leftover, torn, 0o600), errCrash)
		})
		_, err := s.Raise(t.Context(), statement(t, idA, 6, d6, "11:30:00"), noon(t))
		restore()
		if !errors.Is(err, errCrash) {
			t.Fatalf("Raise through a write that died: %v, want the write's error", err)
		}
		if after := readFile(t, filepath.Join(dir, fileA)); after != before {
			t.Errorf("the floor file is %s, want the old floor", after)
		}
		reopened := open(t, dir)
		if f, err := reopened.Raise(t.Context(), statement(t, idA, 6, d6, "11:30:00"), noon(t)); err != nil || f.Serial() != 6 {
			t.Errorf("the raise tried again beside the torn temporary file: %s, %v", describe(f), err)
		}
	})
	t.Run("after the rename", func(t *testing.T) {
		dir, s := raised(t)
		restore := policystate.SetReplace(func(root *os.Root, name string, body []byte, perm fs.FileMode) error {
			return errors.Join(files.ReplaceIn(root, name, body, perm), errCrash)
		})
		_, err := s.Raise(t.Context(), statement(t, idA, 6, d6, "11:30:00"), noon(t))
		restore()
		if !errors.Is(err, errCrash) {
			t.Fatalf("Raise through a write that died: %v, want the write's error", err)
		}
		f, err := open(t, dir).Floor(t.Context(), idA)
		if err != nil || f.Serial() != 6 {
			t.Errorf("Floor after the rename: %s, %v; want serial 6", describe(f), err)
		}
	})
}
