//go:build unix

package pause

import (
	"os"
	"path/filepath"
	"testing"
)

// TestACopyReadThroughARetargetedLinkIsAnotherFile: the pause file's path
// runs through a link to directory A, and a file signed there is taken; a
// copy of it in directory B, read once the link names B, is refused before
// any judge sees it.
func TestACopyReadThroughARetargetedLinkIsAnotherFile(t *testing.T) {
	top := t.TempDir()
	for _, d := range []string{"A", "B"} {
		if err := os.MkdirAll(filepath.Join(top, d, "pause"), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	current := filepath.Join(top, "current")
	if err := os.Symlink(filepath.Join(top, "A"), current); err != nil {
		t.Fatal(err)
	}
	p := &signedPlane{path: filepath.Join(current, "pause", "pause.json")}
	p.write(t, "alice", p.header(t, "alice", 1), paused)
	judged := 0
	counts := judgeFunc(func(f Signed) (SignedBy, error) { judged++; return takes(f) })
	if s := p.read(t, counts); s.State() != Paused || judged != 1 {
		t.Fatalf("the control, the file read where it was signed = %s, %q: %s, judged %d time(s); want paused", s.State(), s.Cause(), s.Detail(), judged)
	}
	raw, err := os.ReadFile(p.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(top, "B", "pause", "pause.json"), raw, 0o640); err != nil { //nolint:gosec // G306: the mode a signed write leaves
		t.Fatal(err)
	}
	if err := os.Remove(current); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(top, "B"), current); err != nil {
		t.Fatal(err)
	}
	wantUnknown(t, "a copy read through the retargeted link", p.read(t, counts), CauseOtherFile)
	if judged != 1 {
		t.Errorf("the copy was handed to the judge, which raises the floor: judged %d time(s), want 1", judged)
	}
}
