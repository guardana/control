package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/guardana/control/internal/trailfile"
)

// maxShownID bounds how much of one identifier a trail line prints.
const maxShownID = 64

// shapeNote is the last line trail prints: evidence.ValidateChain reads the
// links and the order, and a nil from it is not a verification.
const shapeNote = "the check proves each chain's shape and not its integrity: a record altered to keep that shape passes it"

func declareTrail(flags *flag.FlagSet) commandFunc {
	return func(_ context.Context, args []string, stdout, stderr io.Writer) int {
		if len(args) != 1 {
			writeLine(stderr, flags.Name()+": name one trail file to read")
			return exitUsage
		}
		return trail(args[0], stdout, stderr)
	}
}

// trail reads a trail file up to its last newline and prints one line per
// request's trail with the chain check's verdict, then the counts and what the
// check does not prove. It exits 1 when a trail failed or could not be read
// either way, when the file holds no trail at all, since a check of nothing is
// not a pass, and when the file ends in a partial line no collector is
// writing.
func trail(path string, stdout, stderr io.Writer) int {
	rep, err := trailfile.ReadFile(path, trailfile.DefaultMaxLines)
	if err != nil {
		return fail(stderr, "trail", err)
	}
	if len(rep.Trails) == 0 {
		return fail(stderr, "trail", fmt.Errorf("%s holds no trail: %d line(s), %d byte(s) after the last newline",
			strconv.Quote(path), rep.Lines, rep.Unread))
	}
	counts := map[trailfile.Verdict]int{}
	var b strings.Builder
	for _, tr := range rep.Trails {
		counts[tr.Verdict]++
		fmt.Fprintf(&b, "request=%s project=%s tenant=%s last=%s %s", shown(tr.RequestID), shown(tr.ProjectID),
			shown(tr.TenantID), tr.Last, tr.Verdict)
		if tr.Reason != nil {
			b.WriteString(": " + oneLine(tr.Reason.Error()))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "trails %d: ok %d, open %d, failed %d, indeterminate %d; lines %d, repeated lines collapsed %d, bytes after the last newline %d\n",
		len(rep.Trails), counts[trailfile.Passed], counts[trailfile.StillOpen], counts[trailfile.Failed],
		counts[trailfile.Indeterminate], rep.Lines, rep.Duplicates, rep.Unread)
	b.WriteString(shapeNote + "\n")
	if _, err := io.WriteString(stdout, b.String()); err != nil {
		return fail(stderr, "trail", errors.Join(errors.New("writing to standard output"), err))
	}
	if rep.Unread > 0 && !rep.Held {
		return fail(stderr, "trail", fmt.Errorf("%s ends in %d byte(s) after its last newline and no collector holds it: "+
			"no append is under way, so the file is damaged", strconv.Quote(path), rep.Unread))
	}
	if counts[trailfile.Failed]+counts[trailfile.Indeterminate] > 0 {
		return exitFail
	}
	return exitOK
}

// shown is an identifier as a trail line prints it: as it is when it is
// printable ASCII with no space, quote or equals sign, and quoted otherwise,
// so a line break or a terminal's escape in a file never reaches the
// terminal. One longer than maxShownID bytes is cut and says so. Key text in
// it is withheld, as oneLine withholds it.
func shown(id string) string {
	if len(id) > maxShownID {
		return oneLine(strconv.QuoteToASCII(id[:maxShownID])) + " (cut)"
	}
	if id == "" || strings.IndexFunc(id, func(r rune) bool { return r <= ' ' || r >= 0x7f || r == '"' || r == '=' }) >= 0 {
		return oneLine(strconv.QuoteToASCII(id))
	}
	return oneLine(id)
}
