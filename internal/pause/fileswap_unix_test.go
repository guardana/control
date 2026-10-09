//go:build unix

package pause

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// linkAtFileNamed replaces the pause file at path with a link to a sibling
// holding body, once, when a read has judged the name no link, and reports
// whether it did. The sibling passes every check of its own.
func linkAtFileNamed(t *testing.T, path, body string) *bool {
	t.Helper()
	sibling := filepath.Join(filepath.Dir(path), "sibling.json")
	if err := os.WriteFile(sibling, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	swapped := new(bool)
	dirSteps = func(at string) {
		if at != stepFileNamed || *swapped {
			return
		}
		*swapped = true
		link := path + ".link"
		if err := os.Symlink(filepath.Base(sibling), link); err != nil {
			t.Error(err)
		}
		if err := os.Rename(link, path); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { dirSteps = nil })
	return swapped
}

// TestALinkSwappedInAfterTheNameWasJudgedIsNeverRead: the directory handle
// follows a link at the file's own name, so a link put there once the name
// was judged no link would lift the pause through a file that pauses nothing.
// The file opened is not the one judged; the read starts again, finds the
// link at the name and is unknown.
func TestALinkSwappedInAfterTheNameWasJudgedIsNeverRead(t *testing.T) {
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	path := ownPauseFile(t)
	swapped := linkAtFileNamed(t, path, clearBody)
	s := Read(path, now, time.Second)
	if !*swapped {
		t.Fatal("the read never reached the step, so this case examined nothing")
	}
	if s.State() != Unknown || s.Cause() != CauseLink {
		t.Errorf("Read = %s, %q (%s); want unknown, %q", s.State(), s.Cause(), s.Detail(), CauseLink)
	}
}

// replaceAtFileNamed replaces the pause file at path with a regular file
// holding body, as a writer does, each time a read has judged the name, up to
// times times, and counts the replacements.
func replaceAtFileNamed(t *testing.T, path, body string, times int) *int {
	t.Helper()
	replaced := new(int)
	dirSteps = func(at string) {
		if at != stepFileNamed || *replaced >= times {
			return
		}
		*replaced++
		next := path + ".next"
		if err := os.WriteFile(next, []byte(body), 0o600); err != nil {
			t.Error(err)
		}
		if err := os.Rename(next, path); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { dirSteps = nil })
	return replaced
}

// TestAFileAWriterReplacedDuringTheReadIsReadAgain: a writer replacing the
// file between the name's judgement and the open is a change the read starts
// again on, and the file it then reads is the writer's.
func TestAFileAWriterReplacedDuringTheReadIsReadAgain(t *testing.T) {
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	path := ownPauseFile(t)
	replaced := replaceAtFileNamed(t, path, clearBody, 1)
	s := Read(path, now, time.Second)
	if *replaced != 1 {
		t.Fatalf("the file was replaced %d times, want once", *replaced)
	}
	if s.State() != Clear {
		t.Errorf("Read = %s, %q (%s); want the writer's lifted pause", s.State(), s.Cause(), s.Detail())
	}
}

// TestAFileReplacedAtEveryAttemptIsUnknown: a file that changes under every
// attempt is never read, and the read is unknown.
func TestAFileReplacedAtEveryAttemptIsUnknown(t *testing.T) {
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	path := ownPauseFile(t)
	replaced := replaceAtFileNamed(t, path, clearBody, readAttempts)
	s := Read(path, now, time.Second)
	if *replaced != readAttempts {
		t.Fatalf("the file was replaced %d times, want %d", *replaced, readAttempts)
	}
	if s.State() != Unknown || s.Cause() != CauseUnreadable {
		t.Errorf("Read = %s, %q (%s); want unknown, %q", s.State(), s.Cause(), s.Detail(), CauseUnreadable)
	}
}

// TestAWriterRefusesALinkSwappedInAfterTheNameWasJudged: a writer reads the
// file it rewrites through the same check, so it writes back nothing it made
// of the linked file.
func TestAWriterRefusesALinkSwappedInAfterTheNameWasJudged(t *testing.T) {
	path := ownPauseFile(t)
	swapped := linkAtFileNamed(t, path, globalBody)
	err := Remove(context.Background(), path, "g")
	if !*swapped {
		t.Fatal("the write never reached the step, so this case examined nothing")
	}
	if !errors.Is(err, ErrFileChanged) {
		t.Errorf("Remove through a link swapped in = %v, want ErrFileChanged", err)
	}
	at, lerr := os.Lstat(path)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if at.Mode()&os.ModeSymlink == 0 {
		t.Error("the writer replaced the link it read through with a file")
	}
}

// TestOnlyAReplacedFileIsReadAgain: a file refused for anything but a change
// under the read, here a body that does not parse, is read once.
func TestOnlyAReplacedFileIsReadAgain(t *testing.T) {
	now := time.Date(2026, time.September, 24, 12, 0, 0, 0, time.UTC)
	path := ownPauseFile(t)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	named := 0
	dirSteps = func(at string) {
		if at == stepFileNamed {
			named++
		}
	}
	t.Cleanup(func() { dirSteps = nil })
	s := Read(path, now, time.Second)
	if s.State() != Unknown || s.Cause() != CauseMalformed || named != 1 {
		t.Errorf("Read = %s, %q after %d attempt(s); want unknown, %q after one", s.State(), s.Cause(), named, CauseMalformed)
	}
}
