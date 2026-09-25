package gatewayconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPauseDefaultsStandWhereTheFileIsSilent: no pause file, a poll every
// second.
func TestPauseDefaultsStandWhereTheFileIsSilent(t *testing.T) {
	cfg := load(t, write(t, ""))
	if cfg.Pause.File != "" || cfg.Pause.PollInterval != time.Second {
		t.Errorf("pause = %+v; want no file and a poll every 1s", cfg.Pause)
	}
	cfg = load(t, write(t, "\npause:\n  file: pause/pause.json\n  poll_interval: 250ms\n"))
	if cfg.Pause.File != "pause/pause.json" || cfg.Pause.PollInterval != 250*time.Millisecond {
		t.Errorf("pause = %+v", cfg.Pause)
	}
}

// TestThePauseFileIsNotInADirectoryAPlaneLocks: the spool, the approvals
// store and the hold journal each lock their directory, and a writer of the
// pause file locks its own, so a pause file in or under one of them could not
// be written while a plane runs. A directory that holds one of them is not
// locked by it, and is accepted.
func TestThePauseFileIsNotInADirectoryAPlaneLocks(t *testing.T) {
	for _, c := range []struct {
		name string
		file string
		ok   bool
	}{
		{"the spool's own directory", "spool/pause.json", false},
		{"a directory inside the spool's", "spool/p/pause.json", false},
		{"the approvals directory", "approvals/pause.json", false},
		{"a directory inside the approvals directory", "approvals/p/pause.json", false},
		{"the journal's directory", "holds/pause.json", false},
		{"a directory inside the journal's", "holds/p/pause.json", false},
		{"a directory of its own", "pause/pause.json", true},
		{"the directory the spool sits in", "pause.json", true},
		{"a sibling whose name starts like the spool's", "spooled/pause.json", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(write(t, fileProvider+"\npause:\n  file: "+c.file+"\n"), os.Environ())
			switch {
			case c.ok && err != nil:
				t.Errorf("%s was refused: %v", c.file, err)
			case !c.ok && err == nil:
				t.Errorf("%s was accepted", c.file)
			case !c.ok && !strings.Contains(err.Error(), "pause.file"):
				t.Errorf("the refusal does not name the key: %v", err)
			}
		})
	}
}

// TestThePollIntervalIsBoundedAndNeedsAFile: at each bound the interval is
// taken, one step past it refused, and an interval with no pause file is a
// setting that applies to nothing.
func TestThePollIntervalIsBoundedAndNeedsAFile(t *testing.T) {
	withFile := "\npause:\n  file: " + filepath.Join("pause", "pause.json") + "\n"
	for _, c := range []struct {
		value string
		ok    bool
	}{
		{"100ms", true},
		{"99ms", false},
		{"1m", true},
		{"1m1ms", false},
		{"0s", false},
		{"-1s", false},
	} {
		t.Run(c.value, func(t *testing.T) {
			path := write(t, withFile)
			setEnv(t, "pause.poll_interval", c.value)
			_, err := Load(path, os.Environ())
			switch {
			case c.ok && err != nil:
				t.Errorf("%s was refused: %v", c.value, err)
			case !c.ok && err == nil:
				t.Errorf("%s was accepted", c.value)
			case !c.ok && !strings.Contains(err.Error(), "pause.poll_interval"):
				t.Errorf("the refusal does not name the key: %v", err)
			}
		})
	}
	path := write(t, "")
	setEnv(t, "pause.poll_interval", "2s")
	if _, err := Load(path, os.Environ()); err == nil || !strings.Contains(err.Error(), "pause.poll_interval") {
		t.Errorf("an interval with no pause file = %v; want it refused by name", err)
	}
}

// TestAnotherSpellingOfALockedDirectoryIsRefused: the pause file's directory
// is compared with each locked one by identity, so a link to the spool's
// directory, a link to a directory holding it, and a spelling that differs
// only in case on a volume that ignores case are each the spool's directory.
// The same links to a directory no plane locks are accepted, so each refusal
// is the identity's.
func TestAnotherSpellingOfALockedDirectoryIsRefused(t *testing.T) {
	for _, c := range []struct {
		name, file, target string
		ok                 bool
	}{
		{"a link to the spool's directory", "alias/pause.json", "spool", false},
		{"a directory inside a link to it", "alias/p/pause.json", "spool", false},
		{"a link to the approvals directory", "alias/pause.json", "approvals", false},
		{"a link to the journal's directory", "alias/pause.json", "holds", false},
		{"a link to a directory of its own", "alias/pause.json", "pause", true},
		{"a spelling that differs in case", "SPOOL/pause.json", "", false},
		{"a path back into the spool's directory", "./spool/../spool/pause.json", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := lockedTree(t, c.file, c.target)
			if strings.HasPrefix(c.file, "SPOOL") {
				if _, err := os.Stat(filepath.Join(filepath.Dir(path), "SPOOL")); err != nil {
					t.Skip("this volume tells SPOOL from spool, so the case alias names no directory")
				}
			}
			_, err := Load(path, os.Environ())
			switch {
			case c.ok && err != nil:
				t.Errorf("%s was refused: %v", c.file, err)
			case !c.ok && err == nil:
				t.Errorf("%s was accepted", c.file)
			case !c.ok && !strings.Contains(err.Error(), "pause.file"):
				t.Errorf("the refusal does not name the key: %v", err)
			}
		})
	}
}

// lockedTree writes a configuration whose pause file is file beside the
// directories a plane locks, made on disk, and a link alias to target where
// one is named.
func lockedTree(t *testing.T, file, target string) string {
	t.Helper()
	path := write(t, fileProvider+"\npause:\n  file: "+file+"\n")
	root := filepath.Dir(path)
	for _, dir := range []string{"spool", "approvals", "holds", "pause", filepath.Join("spool", "p"), filepath.Join("pause", "p")} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if target != "" {
		if err := os.Symlink(filepath.Join(root, target), filepath.Join(root, "alias")); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
