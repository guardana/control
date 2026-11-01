package segmentlog

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"testing"
)

const errCrash Error = "the process stopped here"

// crashing are the directory steps of a process that stops before its step
// numbered at, counting from 1, and takes none after it. calls counts the
// steps asked for, so a run past the last step says the crash never came.
type crashing struct {
	at, calls int
}

func (c *crashing) ops() fsOps {
	step := func() error {
		c.calls++
		if c.calls >= c.at {
			return errCrash
		}
		return nil
	}
	return fsOps{
		replace: func(root *os.Root, name string, body []byte) error {
			if err := step(); err != nil {
				return err
			}
			return osOps.replace(root, name, body)
		},
		rename: func(root *os.Root, from, to string) error {
			if err := step(); err != nil {
				return err
			}
			return osOps.rename(root, from, to)
		},
		remove: func(root *os.Root, name string) error {
			if err := step(); err != nil {
				return err
			}
			return osOps.remove(root, name)
		},
		sync: func(root *os.Root) error {
			if err := step(); err != nil {
				return err
			}
			return osOps.sync(root)
		},
	}
}

// crashOpen opens the log as a process that stops before its step at. It
// returns the log when the open came through, and whether the crash came.
func crashOpen(t *testing.T, cfg Config, at int) (*segments, bool) {
	t.Helper()
	c := &crashing{at: at}
	l, err := openWith(cfg, c.ops())
	switch {
	case errors.Is(err, errCrash):
		return nil, true
	case err != nil:
		t.Fatalf("an open with its crash at step %d: %v", at, err)
	}
	l.ops = c.ops()
	return l, false
}

// recovered opens the log after a crash, first as a process that crashes
// again at each step its open takes in turn, then as one that does not, and
// returns what that last open visited and the log.
func recovered(t *testing.T, cfg Config) ([]string, *segments) {
	t.Helper()
	for at := 1; ; at++ {
		l, crashed := crashOpen(t, cfg, at)
		if !crashed {
			_ = l.Close()
			break
		}
	}
	return visited(t, cfg)
}

// sealCrashes is the log a crash at step at of a seal leaves: two lines in
// the live segment, then a seal that stops there. A seal stopped at its
// manifest replace changed nothing and leaves the log taking appends; one
// stopped later leaves it refusing them. It reports whether the crash came
// before the seal ended.
func sealCrashes(t *testing.T, cfg Config, at int) bool {
	t.Helper()
	l, _ := crashOpen(t, cfg, 1<<30)
	mustAppend(t, l, "aaa", "bbb")
	c := &crashing{at: at}
	l.ops = c.ops()
	err := l.Seal()
	if err != nil && !errors.Is(err, errCrash) {
		t.Fatalf("a seal with its crash at step %d: %v", at, err)
	}
	switch {
	case err == nil:
	case at == 1 && l.usable() != nil:
		t.Fatalf("a seal stopped before its manifest was replaced left the log refusing: %v", l.usable())
	case at > 1 && !errors.Is(l.Append(context.Background(), []byte("x\n")), ErrFailed):
		t.Fatal("a log a seal stopped part way took another append")
	}
	_ = l.release()
	return err != nil
}

// TestACrashAtEverySealStepIsCompletedOrNeverBegun: a seal stopped before
// each of its steps, and the open after it stopped before each of its own,
// leaves a log that opens with the two lines once each, either still live or
// sealed, located as what they are, and that takes the next append.
func TestACrashAtEverySealStepIsCompletedOrNeverBegun(t *testing.T) {
	crashes := 0
	for at := 1; ; at++ {
		cfg := config(logDir(t), 64, 256)
		if !sealCrashes(t, cfg, at) {
			break
		}
		crashes++
		sealRecovered(t, cfg, at)
	}
	if crashes != 3 {
		t.Errorf("a seal took %d steps a crash could stop before, want 3: the manifest, the rename, the sync", crashes)
	}
}

// sealRecovered checks the log a seal crashed at step at left: the two lines
// once each, live or sealed, and the next append taken.
func sealRecovered(t *testing.T, cfg Config, at int) {
	t.Helper()
	seen, l := recovered(t, cfg)
	if !slices.Equal(seen, []string{"aaa", "bbb"}) {
		t.Errorf("crash at step %d: the open visited %q", at, seen)
	}
	m := l.Manifest()
	got := l.Locate(key(1, "aaa")).State()
	switch {
	case len(m.Segments) == 0 && m.Live.Lines == 2 && got == StateLive:
	case len(m.Segments) == 1 && m.Segments[0].Lines == 2 && m.Live.Lines == 0 && got == StateSealed:
	default:
		t.Errorf("crash at step %d: the log is %+v and aaa is %v", at, m, got)
	}
	mustAppend(t, l, "ccc")
	_ = l.Close()
	seen, l = visited(t, cfg)
	defer func() { _ = l.Close() }()
	if !slices.Equal(seen, []string{"aaa", "bbb", "ccc"}) {
		t.Errorf("crash at step %d: after an append the open visited %q", at, seen)
	}
}

// pruneCrashes is the log a crash at step at of a prune leaves: two sealed
// segments and a live one, then a prune of both sealed ones that stops
// there.
func pruneCrashes(t *testing.T, cfg Config, at int) bool {
	t.Helper()
	l, _ := crashOpen(t, cfg, 1<<30)
	mustAppend(t, l, "aaa", "bbb")
	mustAppend(t, l, "ccc", "ddd")
	mustAppend(t, l, "eee")
	c := &crashing{at: at}
	l.ops = c.ops()
	err := l.Prune(2)
	if err != nil && !errors.Is(err, errCrash) {
		t.Fatalf("a prune with its crash at step %d: %v", at, err)
	}
	_ = l.release()
	return err != nil
}

// TestACrashAtEveryPruneStepIsCompletedOrNeverBegun: a prune stopped before
// each of its steps, and the open after it stopped before each of its own,
// leaves either both sealed segments whole or both pruned with no file
// left, and the live segment whole either way.
func TestACrashAtEveryPruneStepIsCompletedOrNeverBegun(t *testing.T) {
	crashes := 0
	for at := 1; ; at++ {
		cfg := config(logDir(t), 8, 64)
		if !pruneCrashes(t, cfg, at) {
			break
		}
		crashes++
		seen, l := recovered(t, cfg)
		m := l.Manifest()
		files := dirNames(t, cfg.Dir)
		switch {
		case slices.Equal(states(m), []State{StateSealed, StateSealed}) &&
			slices.Equal(seen, []string{"aaa", "bbb", "ccc", "ddd", "eee"}) && len(files) == 5:
		case slices.Equal(states(m), []State{StatePruned, StatePruned}) && slices.Equal(seen, []string{"eee"}) &&
			slices.Equal(files, []string{LiveName, ManifestName, LockName}) && l.Locate(key(2, "ccc")).State() == StatePruned:
		default:
			t.Errorf("crash at step %d: states %v, visited %q, files %v", at, states(m), seen, files)
		}
		_ = l.Close()
	}
	if crashes != 4 {
		t.Errorf("a prune of two segments took %d steps a crash could stop before, want 4", crashes)
	}
}

// TestACrashLeavingAManifestPartWrittenIsRemovedAtOpen: the temporary file a
// crash inside a manifest write leaves is removed by the next open, and the
// manifest it did not replace is the one read.
func TestACrashLeavingAManifestPartWrittenIsRemovedAtOpen(t *testing.T) {
	cfg := config(logDir(t), 64, 256)
	l := openLog(t, cfg)
	mustAppend(t, l, "aaa")
	_ = l.Close()
	tmp := cfg.Dir + "/.tmp-AAAAAAAAAAAAAAAAAAAAAAAAAA"
	if err := os.WriteFile(tmp, []byte(`{"format":`), 0o600); err != nil {
		t.Fatal(err)
	}
	seen, again := visited(t, cfg)
	if !slices.Equal(seen, []string{"aaa"}) {
		t.Errorf("visited %q", seen)
	}
	if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the part-written manifest is still there: %v", err)
	}
	_ = again.Close()
}

// TestASealedSegmentIsWrittenOnceAcrossAReopen: the bytes of a sealed segment
// a crash left complete are the bytes it was sealed with.
func TestASealedSegmentIsWrittenOnceAcrossAReopen(t *testing.T) {
	cfg := config(logDir(t), 64, 256)
	if !sealCrashes(t, cfg, 2) {
		t.Fatal("the seal took fewer than two steps")
	}
	_, l := recovered(t, cfg)
	defer func() { _ = l.Close() }()
	if got := fileText(t, cfg.Dir+"/segment-00000001.jsonl"); !bytes.Equal([]byte(got), []byte("aaa\nbbb\n")) {
		t.Errorf("the completed seal holds %q", got)
	}
}

const errReplace Error = "the manifest replace failed"

// failingReplace are the directory steps with a manifest replace that fails
// once when armed: before it changes anything, or after it renamed the new
// manifest into place.
type failingReplace struct{ before, after bool }

func (f *failingReplace) ops() fsOps {
	ops := osOps
	ops.replace = func(root *os.Root, name string, body []byte) error {
		switch {
		case f.before:
			f.before = false
			return errReplace
		case f.after:
			f.after = false
			return errors.Join(osOps.replace(root, name, body), errReplace)
		}
		return osOps.replace(root, name, body)
	}
	return ops
}

// openFailingReplace opens a new log whose manifest replace fails when armed.
func openFailingReplace(t *testing.T) (*segments, *failingReplace, Config) {
	t.Helper()
	cfg := config(logDir(t), 8, 64)
	f := &failingReplace{}
	l, err := openWith(cfg, f.ops())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, f, cfg
}

// TestAManifestReplaceThatChangedNothingLeavesTheLogUsable: a seal or a
// prune whose manifest replace fails before the new manifest is in place
// returns its error and leaves the log as it was, and the next append or
// prune goes on.
func TestAManifestReplaceThatChangedNothingLeavesTheLogUsable(t *testing.T) {
	l, f, cfg := openFailingReplace(t)
	mustAppend(t, l, "aaa", "bbb")
	f.before = true
	if err := l.Append(context.Background(), []byte("ccc\n")); !errors.Is(err, errReplace) || errors.Is(err, ErrFailed) {
		t.Fatalf("a seal whose manifest was not replaced: %v, want its error and not ErrFailed", err)
	}
	if m := l.Manifest(); len(m.Segments) != 0 || m.Live.Lines != 2 {
		t.Fatalf("a seal that failed changed the log: %+v", m)
	}
	mustAppend(t, l, "ccc", "ddd")
	mustAppend(t, l, "eee")
	f.before = true
	if err := l.Prune(1); !errors.Is(err, errReplace) || errors.Is(err, ErrFailed) {
		t.Fatalf("a prune whose manifest was not replaced: %v, want its error and not ErrFailed", err)
	}
	if got := states(l.Manifest()); !slices.Equal(got, []State{StateSealed, StateSealed}) {
		t.Fatalf("a prune that failed changed the log: %v", got)
	}
	if err := l.Prune(1); err != nil {
		t.Fatalf("the prune again: %v", err)
	}
	if got := dirNames(t, cfg.Dir); slices.Contains(got, segmentFile(1)) {
		t.Errorf("the prune again left segment 1: %v", got)
	}
}

// TestAManifestReplaceThatFailedAfterItsRenameFailsTheLog: a seal whose
// manifest replace fails once the new manifest is in place leaves the log
// refusing appends until it is opened again.
func TestAManifestReplaceThatFailedAfterItsRenameFailsTheLog(t *testing.T) {
	l, f, _ := openFailingReplace(t)
	mustAppend(t, l, "aaa", "bbb")
	f.after = true
	if err := l.Append(context.Background(), []byte("ggg\n")); !errors.Is(err, ErrFailed) {
		t.Fatalf("a seal whose replace failed after the rename: %v, want ErrFailed", err)
	}
	if err := l.Append(context.Background(), []byte("hhh\n")); !errors.Is(err, ErrFailed) {
		t.Errorf("an append after it: %v, want ErrFailed", err)
	}
}
