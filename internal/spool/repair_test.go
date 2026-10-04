package spool

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// journalFile is the file seam with each call written to a journal shared by
// every file the spool opens, and the syncs failing while failing is above
// zero, one fewer each time.
type journalFile struct {
	segmentFile
	j *journal
}

type journal struct {
	mu      sync.Mutex
	calls   []string
	failing int
}

func (j *journal) note(call string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.calls = append(j.calls, call)
}

// since returns the calls after the first n.
func (j *journal) since(n int) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return strings.Join(j.calls[n:], " ")
}

func (j *journal) mark() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.calls)
}

func (j *journal) failSyncs(n int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.failing = n
}

func (f journalFile) Write(p []byte) (int, error) { f.j.note("write"); return f.segmentFile.Write(p) }
func (f journalFile) Close() error                { f.j.note("close"); return f.segmentFile.Close() }

func (f journalFile) Truncate(size int64) error {
	f.j.note("truncate")
	return f.segmentFile.Truncate(size)
}

func (f journalFile) Sync() error {
	f.j.mu.Lock()
	fail := f.j.failing > 0
	if fail {
		f.j.failing--
	}
	f.j.calls = append(f.j.calls, "sync")
	f.j.mu.Unlock()
	if fail {
		return errDiskGone
	}
	return f.segmentFile.Sync()
}

var errDiskGone = errors.New("disk gone")

// journaled opens a spool in a new directory whose files write to j, with one
// record appended and delivered so a quarantine append is covered.
func journaled(t *testing.T, j *journal) (*Spool, *Reader) {
	t.Helper()
	s, err := Open(Options{Dir: t.TempDir(), MaxBytes: 1 << 20, SegmentBytes: 1 << 16,
		openFile: func(root *os.Root, name string, flag int) (segmentFile, error) {
			f, err := openOSFile(root, name, flag)
			if err != nil {
				return nil, err
			}
			return journalFile{segmentFile: f, j: j}, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for i := range 2 {
		if err := s.Append(context.Background(), sample(i)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := s.Reader(Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, err := r.Next(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	return s, r
}

// TestACutAfterAFailedSyncIsForcedToDisk: a record whose sync failed may still
// reach the disk whole, so the cut that takes it back is synced before the
// file is closed; otherwise a power loss can bring back as evidence a record
// the append reported failed. The quarantine is repaired the same way.
func TestACutAfterAFailedSyncIsForcedToDisk(t *testing.T) {
	for name, fail := range map[string]func(*Spool, *Reader) error{
		"segment": func(s *Spool, _ *Reader) error { return s.Append(context.Background(), sample(9)) },
		"quarantine": func(_ *Spool, r *Reader) error {
			_, err := r.Quarantine([]*controlv1.Event{sample(0)})
			return err
		},
	} {
		j := &journal{}
		s, r := journaled(t, j)
		at := j.mark()
		j.failSyncs(1)
		if err := fail(s, r); !errors.Is(err, errDiskGone) {
			t.Fatalf("%s: an append whose sync failed = %v, want the failure", name, err)
		}
		calls := strings.Fields(j.since(at))
		cut := slices.Index(calls, "truncate")
		if cut < 0 || !slices.Equal(calls[cut:], []string{"truncate", "sync", "close"}) {
			t.Errorf("%s: the repair made %q, want the cut synced before the close", name, calls)
		}
		if err := s.Append(context.Background(), sample(10)); err != nil {
			t.Errorf("%s: an append after a repair that reached the disk = %v", name, err)
		}
	}
}

// TestACutThatCannotBeForcedToDiskBreaksTheSpool: when the cut's own sync
// fails too, nobody can say whether the failed record comes back after a
// power loss, so the spool takes nothing more.
func TestACutThatCannotBeForcedToDiskBreaksTheSpool(t *testing.T) {
	for name, fail := range map[string]func(*Spool, *Reader) error{
		"segment": func(s *Spool, _ *Reader) error { return s.Append(context.Background(), sample(9)) },
		"quarantine": func(_ *Spool, r *Reader) error {
			_, err := r.Quarantine([]*controlv1.Event{sample(0)})
			return err
		},
	} {
		j := &journal{}
		s, r := journaled(t, j)
		j.failSyncs(2)
		if err := fail(s, r); err == nil {
			t.Fatalf("%s: an append whose sync failed went through", name)
		}
		j.failSyncs(0)
		if err := s.Append(context.Background(), sample(10)); err == nil || errors.Is(err, ErrFull) {
			t.Errorf("%s: an append after a cut that may not be on disk = %v, want the spool broken", name, err)
		}
	}
}
