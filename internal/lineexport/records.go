package lineexport

import (
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
)

// The records whose members are fixed are written through encoding/json, so
// every string in them is escaped as JSON requires. A line record is written
// by hand, because its line goes out as it stands: encoding/json would
// compact and escape it again.

type headerRecord struct {
	Type    string `json:"type"`
	Format  string `json:"format"`
	Version string `json:"version"`
	File    string `json:"file"`
	Source  string `json:"source,omitempty"`
	Query   any    `json:"query"`
}

type gapRecord struct {
	Type   string `json:"type"`
	Offset int64  `json:"offset"`
	Cursor string `json:"cursor,omitempty"`
	Reason string `json:"reason"`
}

type trailerRecord struct {
	Type         string `json:"type"`
	NextCursor   string `json:"next_cursor,omitempty"`
	EndReached   bool   `json:"end_reached"`
	TailBytes    int64  `json:"tail_bytes"`
	WriterHeld   bool   `json:"writer_held"`
	Counts       counts `json:"counts"`
	ScannedBytes int64  `json:"scanned_bytes"`
	DedupScope   string `json:"dedup_scope"`
	Identity     string `json:"identity,omitempty"`
}

// counts are the trailer's counts in a fixed order: the format's line types,
// then gaps, then duplicates.
type counts struct {
	order []string
	n     map[string]int
}

func (c counts) MarshalJSON() ([]byte, error) {
	b := []byte{'{'}
	for i, name := range c.order {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendInt(append(append(append(b, '"'), name...), `":`...), int64(c.n[name]), 10)
	}
	return append(b, '}'), nil
}

func (x *exporter) header(name string) error {
	h := headerRecord{Type: "header", Format: x.f.Name, Version: x.f.Version, File: name, Query: x.q.Echo}
	if x.identified {
		h.Source = hex.EncodeToString(x.first[:])
	}
	return x.write(h)
}

func (x *exporter) line(t string, offset int64, next string, body []byte) error {
	b := append(append(append([]byte(`{"type":"`), t...), `","offset":`...), strconv.FormatInt(offset, 10)...)
	b = append(append(append(append(append(b, `,"cursor":"`...), next...), `","`...), t...), `":`...)
	b = append(append(b, body...), "}\n"...)
	if _, err := x.out.Write(b); err != nil {
		return err
	}
	x.tr.Counts[t]++
	return nil
}

func (x *exporter) gap(offset int64, next, reason string) error {
	if err := x.write(gapRecord{Type: Gap, Offset: offset, Cursor: next, Reason: reason}); err != nil {
		return err
	}
	x.tr.Counts[Gap]++
	return nil
}

// duplicate writes its record by hand, as its id member is the format's.
func (x *exporter) duplicate(offset int64, id string, first int64) error {
	quoted, err := json.Marshal(id)
	if err != nil {
		return err
	}
	b := append([]byte(`{"type":"duplicate","offset":`), strconv.FormatInt(offset, 10)...)
	b = append(append(append(append(b, `,"`...), x.f.IDMember...), `":`...), quoted...)
	b = append(strconv.AppendInt(append(b, `,"first_offset":`...), first, 10), "}\n"...)
	if _, err := x.out.Write(b); err != nil {
		return err
	}
	x.tr.Counts[Duplicate]++
	return nil
}

func (x *exporter) trailer() error {
	err := x.write(trailerRecord{Type: "trailer", NextCursor: x.tr.NextCursor, EndReached: x.tr.EndReached,
		TailBytes: x.tr.TailBytes, WriterHeld: x.tr.WriterHeld, ScannedBytes: x.tr.ScannedBytes, DedupScope: "export",
		Identity: x.identity,
		Counts:   counts{order: append(slices.Clone(x.f.Lines), Gap, Duplicate), n: x.tr.Counts}})
	if err != nil {
		return err
	}
	return x.out.Flush()
}

func (x *exporter) write(record any) error {
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	_, err = x.out.Write(append(b, '\n'))
	return err
}
