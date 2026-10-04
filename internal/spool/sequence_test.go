package spool_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/spool"
)

// TestAnAppendPastTheSequenceIsRefused: a log whose last record is numbered
// 2^64-2 has no number left for another, so the append is refused and the log
// opens again as it was, instead of holding a record Open reads as corrupt.
func TestAnAppendPastTheSequenceIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		first  string
		taken  bool
		onDisk int
	}{
		"the last number taken": {"18446744073709551613.seg", false, 2},
		"one number left":       {"18446744073709551612.seg", true, 3},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			opts := spool.Options{Dir: dir, MaxBytes: 1 << 20, SegmentBytes: 1 << 16}
			s := open(t, opts)
			mustAppend(t, s, event(controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED, "req-1", 1))
			mustAppend(t, s, event(controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED, "req-1", 2))
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(dir, segmentNames(t, dir)[0]), filepath.Join(dir, c.first)); err != nil {
				t.Fatal(err)
			}
			s = open(t, opts)
			err := s.Append(t.Context(), event(controlv1.EventKind_EVENT_KIND_ACTION_PROPOSED, "req-1", 3))
			if c.taken != (err == nil) || (err != nil && !errors.Is(err, spool.ErrFull)) {
				t.Errorf("Append = %v, want taken %t or ErrFull", err, c.taken)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s = open(t, opts)
			r := reader(t, s)
			deliveredCursors(t, r, c.onDisk)
		})
	}
}
