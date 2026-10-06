package stopwrite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/reaction/stoplist"
)

// Carried is what a carry wrote.
type Carried struct {
	// Header is the new list's header.
	Header reaction.Header
	// Stops is how many stops the new list holds.
	Stops int
	// Covered is how many covered lines it holds.
	Covered int
}

// carrying, when a test sets it, runs once a carry read the old list and
// before it writes the new one.
var carrying func()

// Carry starts a stop list in dir, judged against route, from the list in
// from, judged against fromRoute: the new list holds every stop of the old
// one no lift ended, expired or not, as it was, since the writer's clock may
// be one a plane would not trust, and a covered line for every other finding
// the old list names, so that a finding the old list names never stops a run
// again. The old list's lock is held until the new list is written, so no
// write to it falls between the two. A carry writes the new list whole or not
// at all, and refuses with ErrExists when dir holds a list.
func Carry(ctx context.Context, from string, fromRoute reaction.Route, dir string, route reaction.Route, now time.Time) (Carried, error) {
	var out Carried
	err := locked(ctx, from, func(root *os.Root) error {
		same, err := sameDir(root, dir)
		if err != nil {
			return err
		}
		if same {
			return fmt.Errorf("%w: %s", ErrExists, dir)
		}
		old, err := readList(root)
		if err != nil {
			return err
		}
		list, err := reaction.Judge(fromRoute, reaction.Prefix{}, old, now, 0)
		if err != nil {
			return fmt.Errorf("the list carried from: %w", refusal(err))
		}
		h, err := newHeader(route)
		if err != nil {
			return err
		}
		header, err := h.Marshal()
		if err != nil {
			return fmt.Errorf("%w: %w", ErrRefused, err)
		}
		out = Carried{Header: h}
		lines := [][]byte{header}
		kept := map[string]bool{}
		for _, e := range list.Entries() {
			line, err := e.Marshal()
			if err != nil {
				return fmt.Errorf("%w: %w", ErrRefused, err)
			}
			lines = append(lines, line)
			kept[e.FindingID] = true
			out.Stops++
		}
		covered, err := coveredLines(old, kept)
		if err != nil {
			return err
		}
		out.Covered = len(covered)
		if carrying != nil {
			carrying()
		}
		return create(ctx, dir, route, now, append(lines, covered...))
	})
	if err != nil {
		return Carried{}, err
	}
	return out, nil
}

// sameDir reports whether dir names the directory root holds. A dir that
// cannot be read is not that directory; the write into it reports why.
func sameDir(root *os.Root, dir string) (bool, error) {
	held, err := root.Stat(".")
	if err != nil {
		return false, err
	}
	named, err := os.Stat(dir)
	return err == nil && os.SameFile(held, named), nil
}

// coveredLines is a covered line for each finding a stop or covered line of
// old names that kept does not.
func coveredLines(old []byte, kept map[string]bool) ([][]byte, error) {
	var covered [][]byte
	complete := old[:bytes.LastIndexByte(old, '\n')+1]
	for raw := range bytes.Lines(complete) {
		l, err := reaction.ParseLine(bytes.TrimSuffix(raw, []byte{'\n'}))
		if err != nil {
			return nil, fmt.Errorf("the list carried from: %w", refusal(err))
		}
		var c reaction.Covered
		switch l.Kind {
		case reaction.KindStop:
			c = reaction.Covered{FindingID: l.Stop.FindingID, TenantID: l.Stop.TenantID, RunID: l.Stop.RunID, CreatedAt: l.Stop.CreatedAt}
		case reaction.KindCovered:
			c = l.Covered
		default:
			continue
		}
		if kept[c.FindingID] {
			continue
		}
		line, err := c.Marshal()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrRefused, err)
		}
		covered = append(covered, line)
	}
	return covered, nil
}

// readList reads the list root holds, with every check a plane's read makes;
// the caller holds the lock.
func readList(root *os.Root) ([]byte, error) {
	named, err := stoplist.Named(root)
	if err != nil {
		return nil, err
	}
	f, err := root.OpenFile(stoplist.FileName, os.O_RDONLY|stoplist.OpenFlags, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if err := stoplist.CheckOpened(named, f); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(f, reaction.MaxListBytes+1))
}
