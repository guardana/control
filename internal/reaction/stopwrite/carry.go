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

// Carry starts a stop list in dir, judged against route, from the list in
// from, judged against fromRoute: the new list holds the old one's stops no
// lift ended and that are not expired at the writer's clock now, as they
// were, and a covered line for every other finding the old list names, so
// that a finding the old list names never stops a run again. A carry writes
// the new list whole or not at all, and refuses with ErrExists when dir holds
// a list.
func Carry(ctx context.Context, from string, fromRoute reaction.Route, dir string, route reaction.Route, now time.Time) (Carried, error) {
	old, err := readLocked(ctx, from)
	if err != nil {
		return Carried{}, err
	}
	list, err := reaction.Judge(fromRoute, reaction.Prefix{}, old, now, 0)
	if err != nil {
		return Carried{}, fmt.Errorf("the list carried from: %w", refusal(err))
	}
	h, err := newHeader(route)
	if err != nil {
		return Carried{}, err
	}
	header, err := h.Marshal()
	if err != nil {
		return Carried{}, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	out := Carried{Header: h}
	lines := [][]byte{header}
	kept := map[string]bool{}
	for _, e := range list.Entries() {
		if !now.Before(e.ExpiresAt) {
			continue
		}
		line, err := e.Marshal()
		if err != nil {
			return Carried{}, fmt.Errorf("%w: %w", ErrRefused, err)
		}
		lines = append(lines, line)
		kept[e.FindingID] = true
		out.Stops++
	}
	covered, err := coveredLines(old, kept)
	if err != nil {
		return Carried{}, err
	}
	out.Covered = len(covered)
	if err := create(ctx, dir, route, now, append(lines, covered...)); err != nil {
		return Carried{}, err
	}
	return out, nil
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

// readLocked reads the list in dir under its lock, with every check a plane's
// read makes, so no writer appends to it while it is read.
func readLocked(ctx context.Context, dir string) ([]byte, error) {
	var content []byte
	err := locked(ctx, dir, func(root *os.Root) error {
		named, err := stoplist.Named(root)
		if err != nil {
			return err
		}
		f, err := root.OpenFile(stoplist.FileName, os.O_RDONLY|stoplist.OpenFlags, 0)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		if err := stoplist.CheckOpened(named, f); err != nil {
			return err
		}
		content, err = io.ReadAll(io.LimitReader(f, reaction.MaxListBytes+1))
		return err
	})
	return content, err
}
