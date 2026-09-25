//go:build unix

package files_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/guardana/control/internal/files"
)

func TestOpenDirOpensADirectory(t *testing.T) {
	dir := t.TempDir()
	d, err := files.OpenDir(dir)
	if err != nil {
		t.Fatalf("OpenDir(a directory) = %v", err)
	}
	defer func() { _ = d.Close() }()
	info, err := d.Stat()
	if err != nil || !info.IsDir() {
		t.Errorf("the handle is %v (%v), want the directory", info, err)
	}
}

// TestOpenDirRefusesEverythingElse: a link is refused even when it names a
// directory, and a named pipe with no writer is refused at once.
func TestOpenDirRefusesEverythingElse(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(parent, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(parent, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{
		"a link to a directory": link,
		"a regular file":        file,
		"a named pipe":          pipe,
	} {
		done := make(chan error, 1)
		go func() {
			d, err := files.OpenDir(path)
			if err == nil {
				_ = d.Close()
			}
			done <- err
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("OpenDir(%s) opened it", name)
			}
		case <-time.After(5 * time.Second):
			w, err := os.OpenFile(pipe, os.O_WRONLY, 0) //nolint:gosec // G304: the test's own pipe, opened to release the reader
			if err == nil {
				_ = w.Close()
			}
			<-done
			t.Errorf("OpenDir(%s) waited for a writer", name)
		}
	}
}
