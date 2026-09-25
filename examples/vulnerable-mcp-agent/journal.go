package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// entry is one thing a victim received: a tool call with its arguments, or a
// question put to the decision point.
type entry struct {
	Call  string          `json:"call,omitempty"`
	Args  json.RawMessage `json:"args,omitempty"`
	Asked string          `json:"asked,omitempty"`
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
	j := &journal{}
	if dir == "" {
		return j, nil
	}
	f, err := os.OpenFile(filepath.Join(dir, name+".jsonl"), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) //nolint:gosec // G304: a directory the operator named for this journal
	if err != nil {
		return nil, fmt.Errorf("opening the journal: %w", err)
	}
	j.file = f
	return j, nil
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
