package spool_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/spool"
)

// TestATornWriteEndsTheLog is the property ADR-0014 names: cut a spool at any
// byte, reopen it, and every record before the cut replays intact while
// nothing after it does. The cut is modelled two ways, because a disk does
// both: the file ends at the cut, or it keeps its length and the bytes past
// the cut are whatever was there before. The second is what the checksum is
// for; a length check alone accepts it.
func TestATornWriteEndsTheLog(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		dir := t.TempDir()
		n := rapid.IntRange(1, 12).Draw(rt, "records")
		segmentBytes := int64(rapid.IntRange(1, 600).Draw(rt, "segmentBytes"))
		opts := spool.Options{Dir: dir, MaxBytes: 1 << 20, SegmentBytes: segmentBytes}

		written := fillSpool(rt, opts, n)

		// The log as bytes on disk, file by file, and the record boundaries
		// inside it, each computed from the event and the format, not read
		// back from the spool.
		names := segmentNames(t, dir)
		files, total := readFiles(rt, dir, names)
		var ends []int64
		var at int64
		for _, ev := range written {
			at += recordSize(t, ev)
			ends = append(ends, at)
		}
		if at != total {
			rt.Fatalf("the files hold %d bytes, the records account for %d", total, at)
		}

		cut := int64(rapid.IntRange(0, int(total)).Draw(rt, "cut"))
		garbage := rapid.Bool().Draw(rt, "garbage")
		onDisk := cutFiles(rt, dir, names, files, cut, garbage)
		intact, wantTruncated := expectAfterCut(ends, cut, onDisk)

		s, err := spool.Open(opts)
		if err != nil {
			rt.Fatalf("reopen after a cut at %d: %v", cut, err)
		}
		defer s.Close() //nolint:errcheck // the assertions are above
		st, err := s.Stats()
		if err != nil {
			rt.Fatal(err)
		}
		if st.Truncated != wantTruncated {
			rt.Fatalf("cut at %d of %d (garbage=%v): Truncated = %d, want %d", cut, total, garbage, st.Truncated, wantTruncated)
		}
		r, err := s.Reader(spool.Cursor{})
		if err != nil {
			rt.Fatal(err)
		}
		expectRecords(t, r, written[:intact])
		if t.Failed() {
			rt.Fatalf("cut at %d of %d (garbage=%v): the replay differs from the %d records before the cut", cut, total, garbage, intact)
		}

		// The next append starts clean and replays after the survivors.
		fresh := event(controlv1.EventKind_EVENT_KIND_FINDING_RAISED, "", 1000)
		if err := s.Append(context.Background(), fresh); err != nil {
			rt.Fatalf("Append after the cut: %v", err)
		}
		if got, _, err := r.Next(contextWithin(t, 5*time.Second)); err != nil || !proto.Equal(got, fresh) {
			rt.Fatalf("Next after the fresh append = %v, %v", got, err)
		}
	})
}

// expectAfterCut counts the records whose end lies at or before the cut, and
// what Open has to discard: whatever is on disk past the last of them.
func expectAfterCut(ends []int64, cut, onDisk int64) (int, int64) {
	intact := 0
	for _, end := range ends {
		if end <= cut {
			intact++
		}
	}
	truncated := onDisk
	if intact > 0 {
		truncated -= ends[intact-1]
	}
	return intact, truncated
}

// fillSpool opens a spool on opts, appends n events with a drawn run id and
// closes it, returning what it wrote.
func fillSpool(rt *rapid.T, opts spool.Options, n int) []*controlv1.Event {
	s, err := spool.Open(opts)
	if err != nil {
		rt.Fatal(err)
	}
	var written []*controlv1.Event
	for i := range n {
		ev := event(controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED, "req-1", i)
		ev.RunId = rapid.StringMatching(`[a-z]{0,40}`).Draw(rt, "runId")
		if err := s.Append(context.Background(), ev); err != nil {
			rt.Fatal(err)
		}
		written = append(written, ev)
	}
	if err := s.Close(); err != nil {
		rt.Fatal(err)
	}
	return written
}

func readFiles(rt *rapid.T, dir string, names []string) ([][]byte, int64) {
	var files [][]byte
	var total int64
	for _, name := range names {
		b, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: a segment the test itself wrote under its temp dir
		if err != nil {
			rt.Fatal(err)
		}
		files = append(files, b)
		total += int64(len(b))
	}
	return files, total
}

// cutFiles applies a cut at byte `cut` of the concatenated files: the file it
// lands in either ends there or keeps its length with the rest overwritten by
// bytes that differ from the original, and every later file is removed. It
// returns how many bytes of the concatenation are left on disk.
func cutFiles(rt *rapid.T, dir string, names []string, files [][]byte, cut int64, garbage bool) int64 {
	var onDisk, offset int64
	found := false
	for i, b := range files {
		path := filepath.Join(dir, names[i])
		switch {
		case found:
			if err := os.Remove(path); err != nil {
				rt.Fatal(err)
			}
		case cut < offset+int64(len(b)):
			found = true
			in := cut - offset
			kept := append([]byte(nil), b[:in]...)
			if garbage {
				for _, c := range b[in:] {
					kept = append(kept, c^0xff)
				}
			}
			if err := os.WriteFile(path, kept, 0o600); err != nil {
				rt.Fatal(err)
			}
			onDisk += int64(len(kept))
		default:
			onDisk += int64(len(b))
		}
		offset += int64(len(b))
	}
	return onDisk
}
