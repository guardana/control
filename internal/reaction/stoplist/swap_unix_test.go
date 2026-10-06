//go:build unix

package stoplist

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// atStep runs act once, when a read reaches step, and reports whether it did.
func atStep(t *testing.T, step string, act func()) *bool {
	t.Helper()
	done := new(bool)
	steps = func(at string) {
		if at != step || *done {
			return
		}
		*done = true
		act()
	}
	t.Cleanup(func() { steps = nil })
	return done
}

// decoy makes a directory beside dir, holding a list of content that passes
// every check of its own.
func decoy(t *testing.T, dir string, content []byte) string {
	t.Helper()
	d := filepath.Join(filepath.Dir(dir), "decoy")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	writeList(t, d, content)
	return d
}

func rename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Error(err)
	}
}

// TestAReadGoesThroughWhatItJudged: a directory swapped in between the two
// opens of the judged one is refused; one swapped in once the judged one is
// rooted is never read; a link or another file put at the list's name once
// the name was judged is refused. Each swap brings a list that passes every
// check of its own and differs from the judged one.
func TestAReadGoesThroughWhatItJudged(t *testing.T) {
	judged := stoppingList(t)
	other := lines(headerLine(t, testRoute(t), "list-2"))
	for _, c := range []struct {
		name string
		step string
		swap func(t *testing.T, dir string) func()
		err  Error
	}{
		{"the directory, between the opens", stepOpened, swapDir(other), ErrDirChanged},
		{"the directory, once rooted", stepRooted, swapDir(other), ""},
		{"a link at the name", stepFileNamed, func(t *testing.T, dir string) func() {
			if err := os.WriteFile(filepath.Join(dir, "sibling.jsonl"), other, 0o600); err != nil {
				t.Fatal(err)
			}
			return func() {
				link := filepath.Join(dir, "link")
				if err := os.Symlink("sibling.jsonl", link); err != nil {
					t.Error(err)
				}
				rename(t, link, filepath.Join(dir, FileName))
			}
		}, ErrFileChanged},
		{"another file at the name", stepFileNamed, func(t *testing.T, dir string) func() {
			sibling := filepath.Join(dir, "sibling.jsonl")
			if err := os.WriteFile(sibling, other, 0o600); err != nil {
				t.Fatal(err)
			}
			return func() { rename(t, sibling, filepath.Join(dir, FileName)) }
		}, ErrFileChanged},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := listDir(t, judged)
			done := atStep(t, c.step, c.swap(t, dir))
			got, err := Read(dir)
			if !*done {
				t.Fatal("the read never reached the step, so this case examined nothing")
			}
			if c.err != "" {
				if !errors.Is(err, c.err) || CauseOf(err) != CauseUnreadable {
					t.Errorf("Read = %v, want %q, unreadable", err, c.err)
				}
				return
			}
			if err != nil || !bytes.Equal(got, judged) {
				t.Errorf("Read = %q, %v; want the judged directory's list", got, err)
			}
		})
	}
}

// swapDir puts a directory holding content in dir's place.
func swapDir(content []byte) func(t *testing.T, dir string) func() {
	return func(t *testing.T, dir string) func() {
		d := decoy(t, dir, content)
		return func() {
			rename(t, dir, dir+".judged")
			rename(t, d, dir)
		}
	}
}
