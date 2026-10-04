package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/guardana/control/internal/files"
)

// entry is one tool call a victim received, with its arguments.
type entry struct {
	Call string          `json:"call,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
}

// journal keeps what a victim received, in memory and, when it was opened
// with a directory, as JSON lines in a file there.
type journal struct {
	mu      sync.Mutex
	entries []entry
	file    *os.File
}

// openJournal opens the journal of the server name, appending to
// <dir>/<name>.jsonl when dir is not empty.
func openJournal(dir, name string) (*journal, error) {
	return openJournalOf(dir, name, os.Geteuid())
}

// openJournalOf opens the journal as openJournal does, in a file uid owns.
// The file holds what each call carried, so the open follows no link at its
// name and waits on no pipe, and the file is refused unless it has one name,
// uid owns it and no other account may read or write it.
func openJournalOf(dir, name string, uid int) (*journal, error) {
	j := &journal{}
	if dir == "" {
		return j, nil
	}
	path := filepath.Join(dir, name+".jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|journalFlags, 0o600) //nolint:gosec // G304: a directory the operator named for this journal
	if err != nil {
		return nil, fmt.Errorf("opening the journal: %w", err)
	}
	if err := checkJournal(f, uid); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("the journal %s is refused: %w", path, err)
	}
	j.file = f
	return j, nil
}

func checkJournal(f *os.File, uid int) error {
	info, err := f.Stat()
	switch {
	case err != nil:
		return err
	case !info.Mode().IsRegular():
		return errors.New("it is not a regular file")
	case !singleName(info):
		return errors.New("it has a second name, or this platform cannot say it has one")
	case info.Mode().Perm()&0o077 != 0:
		return fmt.Errorf("its mode %v lets another account reach it", info.Mode().Perm())
	}
	return files.CheckOwnedBy(info, uid)
}

// record keeps e, and fails when the file will not take it, so nothing is
// served that the journal lacks.
func (j *journal) record(e entry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.file != nil {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if _, err := j.file.Write(append(line, '\n')); err != nil {
			return fmt.Errorf("writing the journal: %w", err)
		}
	}
	j.entries = append(j.entries, e)
	return nil
}

// received is every entry so far, in the order it came.
func (j *journal) received() []entry {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]entry(nil), j.entries...)
}

func (j *journal) close() error {
	if j.file == nil {
		return nil
	}
	return j.file.Close()
}
