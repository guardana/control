package main

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/segmentlog"
	"github.com/guardana/control/internal/trailfile"
)

const pruneForm = "--through <segment> <dir>"

// trailSegments prints what the manifest of a directory of segments names:
// the counts of sealed, pruned and dropped segments against the most a
// manifest holds, then one line per segment and one for the live segment,
// each with the key a cursor names it by, reading and writing nothing else.
// It exits 1 when the directory is no log this build reads.
func trailSegments(dir string, stdout, stderr io.Writer) int {
	v, err := segmentlog.Inspect(dir, evidence.MaxLineBytes)
	if err != nil {
		return fail(stderr, "trail segments", err)
	}
	held, err := v.Held()
	if err != nil {
		return fail(stderr, "trail segments", err)
	}
	m := v.Manifest()
	collector := "none"
	if held {
		collector = "holds it"
	}
	sealed := 0
	for _, s := range m.Segments {
		if s.State == segmentlog.StateSealed {
			sealed++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "tenant=%s project=%s retained_bytes=%d collector=%s\n", shown(m.Tenant), shown(m.Project), m.Retained, collector)
	fmt.Fprintf(&b, "segments sealed=%d pruned=%d dropped=%d most=%d\n", sealed, len(m.Segments)-sealed, m.Dropped,
		segmentlog.MaxSegments)
	for _, s := range m.Segments {
		k := segmentlog.SegmentKey(s.Seq, s.First)
		fmt.Fprintf(&b, "segment=%d state=%s lines=%d bytes=%d key=%s sealed_at=%s", s.Seq, s.State, s.Lines, s.Bytes,
			hex.EncodeToString(k[:]), s.SealedAt.Format(time.RFC3339))
		if s.State == segmentlog.StatePruned {
			b.WriteString(" pruned_at=" + s.PrunedAt.Format(time.RFC3339))
		}
		b.WriteString("\n")
	}
	key := "none"
	if m.Live.Lines > 0 {
		k := segmentlog.SegmentKey(m.Live.Seq, m.Live.First)
		key = hex.EncodeToString(k[:])
	}
	fmt.Fprintf(&b, "segment=%d state=live lines=%d bytes=%d key=%s tail_bytes=%d\n", m.Live.Seq, m.Live.Lines,
		m.Live.Bytes, key, v.Tail())
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		return fail(stderr, "trail segments", errors.Join(errors.New("writing to standard output"), err))
	}
	return exitOK
}

func declarePrune(flags *flag.FlagSet) commandFunc {
	through := flags.Int("through", 0, "prune every sealed segment up to and including this `segment` number")
	return func(_ context.Context, args []string, stdout, stderr io.Writer) int {
		switch {
		case *through < 1:
			writeLine(stderr, flags.Name()+": --through names the last sealed segment to prune, from 1")
			return exitUsage
		case len(args) != 1:
			writeLine(stderr, flags.Name()+": name one directory of trail segments")
			return exitUsage
		}
		return prune(args[0], *through, stdout, stderr)
	}
}

// prune removes the sealed segments of the log in dir through the one
// numbered through, as an operator decides: each one's tombstone, keeping
// its lines, bytes and the digests of its first and last line, is written
// before its file is unlinked. A collector holding the log is refused, so it
// is stopped first.
func prune(dir string, through int, stdout, stderr io.Writer) int {
	v, err := segmentlog.Inspect(dir, evidence.MaxLineBytes)
	if err != nil {
		return fail(stderr, "prune", err)
	}
	m := v.Manifest()
	l, err := trailfile.OpenLog(trailfile.LogConfig{Dir: dir, Tenant: m.Tenant, Project: m.Project})
	if err != nil {
		return fail(stderr, "prune", fmt.Errorf("%s: %w", strconv.Quote(dir), err))
	}
	before := l.Manifest().Retained
	err = l.Prune(through)
	after := l.Manifest().Retained
	if err = errors.Join(err, l.Close()); err != nil {
		return fail(stderr, "prune", err)
	}
	if _, err := fmt.Fprintf(stdout, "prune: segments through %d are pruned; %d byte(s) removed, %d retained\n",
		through, before-after, after); err != nil {
		return fail(stderr, "prune", fmt.Errorf("writing to standard output: %w", err))
	}
	return exitOK
}
