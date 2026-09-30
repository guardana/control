package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func checkHeader(l rawLine) error {
	if l.tooLong || l.unterminated {
		return errors.New("the first record is not a whole header")
	}
	if err := strictHeader(l.b); err != nil {
		return err
	}
	var h struct {
		Type    string  `json:"type"`
		Format  *string `json:"format"`
		Version *string `json:"version"`
		File    *string `json:"file"`
		Source  *string `json:"source"`
		Query   *struct {
			After                               *string
			Limit                               *int
			MaxBytes                            *int64 `json:"max_bytes"`
			Request, Run, Tenant, Project, Kind []string
		} `json:"query"`
	}
	if err := json.Unmarshal(l.b, &h); err != nil {
		return fmt.Errorf("the first record is not a header: %w", err)
	}
	switch {
	case h.Type != "header":
		return fmt.Errorf("the first record is %q, not the header", h.Type)
	case h.Format == nil || *h.Format != exportFormat:
		return fmt.Errorf("the header names format %s, and this reader reads %s", quoted(h.Format), exportFormat)
	case h.Version == nil:
		return errors.New("the header names no version")
	}
	major, ok := majorOf(*h.Version)
	switch {
	case !ok:
		return fmt.Errorf("version %q is not MAJOR.MINOR", *h.Version)
	case major != exportMajor:
		return fmt.Errorf("version %q is of major %d, and this reader reads major %d", *h.Version, major, exportMajor)
	}
	return nil
}

// majorOf reads MAJOR.MINOR as the wire contracts spell a version: two
// unsigned decimals. A higher minor is read like its major.
func majorOf(version string) (uint64, bool) {
	major, minor, ok := strings.Cut(version, ".")
	m, errMajor := strconv.ParseUint(major, 10, 32)
	_, errMinor := strconv.ParseUint(minor, 10, 32)
	return m, ok && errMajor == nil && errMinor == nil
}

func quoted(s *string) string {
	if s == nil {
		return "none"
	}
	return strconv.Quote(*s)
}

type trailerRecord struct {
	NextCursor   *string `json:"next_cursor"`
	EndReached   *bool   `json:"end_reached"`
	TailBytes    *int64  `json:"tail_bytes"`
	WriterHeld   *bool   `json:"writer_held"`
	ScannedBytes *int64  `json:"scanned_bytes"`
	DedupScope   *string `json:"dedup_scope"`
	Counts       *struct {
		Event     *int `json:"event"`
		Gap       *int `json:"gap"`
		Duplicate *int `json:"duplicate"`
	} `json:"counts"`
}

func (x *export) readTrailer(b []byte, ms []member) {
	if counts, ok := valueOf(ms, "counts"); ok {
		if err := strictObject(counts, countMembers); err != nil {
			x.refuse("a trailer whose counts the contract does not give: " + err.Error())
			return
		}
	}
	var r trailerRecord
	if err := json.Unmarshal(b, &r); err != nil {
		x.refuse("a trailer that does not decode: " + err.Error())
		return
	}
	if why := trailerDefect(r); why != "" {
		x.refuse(why)
		return
	}
	if why := x.countsDefect(r); why != "" {
		x.refuse(why)
		return
	}
	x.trailer = &trailerState{endReached: *r.EndReached, tailBytes: *r.TailBytes, writerHeld: *r.WriterHeld}
}

// trailerDefect names what keeps a trailer from saying where the export
// stopped and what follows it.
func trailerDefect(r trailerRecord) string {
	switch {
	case r.EndReached == nil:
		return "a trailer without end_reached"
	case r.TailBytes == nil:
		return "a trailer without a tail_bytes count"
	case *r.TailBytes < 0:
		return fmt.Sprintf("a trailer whose tail_bytes is %d", *r.TailBytes)
	case r.WriterHeld == nil:
		return "a trailer without writer_held"
	case r.NextCursor != nil && !isCursor(*r.NextCursor):
		return fmt.Sprintf("a trailer whose next_cursor %s is not a v1 cursor", safe(*r.NextCursor))
	case !*r.EndReached && r.NextCursor == nil:
		return "a trailer that stops before the file's end and names no next_cursor"
	}
	return ""
}

// countsDefect names trailer counts that are missing or not the records read.
func (x *export) countsDefect(r trailerRecord) string {
	c := r.Counts
	switch {
	case c == nil || c.Event == nil || c.Gap == nil || c.Duplicate == nil:
		return "a trailer without its counts"
	case *c.Event != x.seenEvents || *c.Gap != x.seenGaps || *c.Duplicate != x.seenDuplicates:
		return fmt.Sprintf("the trailer counts %d event, %d gap and %d duplicate record(s), and the export held %d, %d and %d",
			*c.Event, *c.Gap, *c.Duplicate, x.seenEvents, x.seenGaps, x.seenDuplicates)
	}
	return ""
}
