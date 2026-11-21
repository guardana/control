package files_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/guardana/control/internal/files"
)

func lstat(t *testing.T, path string) fs.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func readBody(t *testing.T, path string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

// TestReplaceJudgedReplacesOnlyTheFileJudged: the file judged is replaced
// whole with the mode asked; a file renamed over the name after the
// judgement is left with its bytes and refused; and the directory is left
// holding the one name either way.
func TestReplaceJudgedReplacesOnlyTheFileJudged(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	writeFile(t, out, []byte("judged"), 0o600)
	judged := lstat(t, out)
	if err := files.ReplaceJudged(dir, "out", []byte("new"), 0o644, judged); err != nil {
		t.Fatalf("the control, the judged file still there: %v", err)
	}
	if got := readBody(t, out); got != "new" || mode(t, out) != 0o644 {
		t.Errorf("the judged file was replaced with %q, mode %04o; want new, 0644", got, mode(t, out))
	}

	judged = lstat(t, out)
	writeFile(t, filepath.Join(dir, "unrelated"), []byte("never judged"), 0o600)
	if err := os.Rename(filepath.Join(dir, "unrelated"), out); err != nil {
		t.Fatal(err)
	}
	err := files.ReplaceJudged(dir, "out", []byte("newer"), 0o644, judged)
	if !errors.Is(err, files.ErrNotJudged) {
		t.Errorf("a replace over a file renamed in after the judgement = %v, want ErrNotJudged", err)
	}
	if got := readBody(t, out); got != "never judged" {
		t.Errorf("the file renamed in now holds %q", got)
	}
	if got := entries(t, dir); len(got) != 1 {
		t.Errorf("the directory holds %v after the refusal, want the one name", got)
	}
}

// TestReplaceJudgedRefusesAFileGoneOrComeSinceTheJudgement: a file judged
// that is gone is not written again, and a name judged absent that a file
// took since is refused and left as it was; the control, a name still
// absent, is created.
func TestReplaceJudgedRefusesAFileGoneOrComeSinceTheJudgement(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out")
	writeFile(t, out, []byte("judged"), 0o600)
	judged := lstat(t, out)
	if err := os.Remove(out); err != nil {
		t.Fatal(err)
	}
	if err := files.ReplaceJudged(dir, "out", []byte("new"), 0o644, judged); !errors.Is(err, files.ErrNotJudged) {
		t.Errorf("a replace of a file gone since the judgement = %v, want ErrNotJudged", err)
	}
	if got := entries(t, dir); len(got) != 0 {
		t.Errorf("the directory holds %v, want nothing", got)
	}
	if err := files.ReplaceJudged(dir, "out", []byte("new"), 0o644, nil); err != nil {
		t.Fatalf("the control, a name judged absent and still absent: %v", err)
	}
	if got := readBody(t, out); got != "new" {
		t.Errorf("the name judged absent holds %q, want new", got)
	}
	if err := files.ReplaceJudged(dir, "out", []byte("newer"), 0o644, nil); !errors.Is(err, files.ErrNotJudged) {
		t.Errorf("a name judged absent that a file took since = %v, want ErrNotJudged", err)
	}
	if got := readBody(t, out); got != "new" {
		t.Errorf("the file that took the name now holds %q", got)
	}
}

// TestReplaceJudgedReplacesALinkJudgedAsALink: the entry judged is the link
// at the name, which is replaced, and the file it named keeps its bytes.
func TestReplaceJudgedReplacesALinkJudgedAsALink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside")
	writeFile(t, outside, []byte("not yours"), 0o600)
	out := filepath.Join(dir, "out")
	if err := os.Symlink(outside, out); err != nil {
		t.Fatal(err)
	}
	if err := files.ReplaceJudged(dir, "out", []byte("new"), 0o644, lstat(t, outside)); !errors.Is(err, files.ErrNotJudged) {
		t.Errorf("a link judged by the file it names = %v, want ErrNotJudged", err)
	}
	if err := files.ReplaceJudged(dir, "out", []byte("new"), 0o644, lstat(t, out)); err != nil {
		t.Fatalf("the link judged as a link: %v", err)
	}
	if got := readBody(t, outside); got != "not yours" {
		t.Errorf("the link's target now holds %q", got)
	}
	if !lstat(t, out).Mode().IsRegular() {
		t.Error("the name is not a regular file after the replace")
	}
}
