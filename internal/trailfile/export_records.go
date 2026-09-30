package trailfile

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// The records other than an event are written through encoding/json, so every
// string in them is escaped as JSON requires. An event is written by hand,
// because its line goes out as it stands: encoding/json would compact and
// escape it again.

type queryRecord struct {
	After    string   `json:"after,omitempty"`
	Limit    int      `json:"limit"`
	MaxBytes int64    `json:"max_bytes,omitempty"`
	Request  []string `json:"request,omitempty"`
	Run      []string `json:"run,omitempty"`
	Tenant   []string `json:"tenant,omitempty"`
	Project  []string `json:"project,omitempty"`
	Kind     []string `json:"kind,omitempty"`
}

type headerRecord struct {
	Type    string      `json:"type"`
	Format  string      `json:"format"`
	Version string      `json:"version"`
	File    string      `json:"file"`
	Source  string      `json:"source,omitempty"`
	Query   queryRecord `json:"query"`
}

type gapRecord struct {
	Type   string `json:"type"`
	Offset int64  `json:"offset"`
	Cursor string `json:"cursor,omitempty"`
	Reason string `json:"reason"`
}

type duplicateRecord struct {
	Type        string `json:"type"`
	Offset      int64  `json:"offset"`
	EventID     string `json:"event_id"`
	FirstOffset int64  `json:"first_offset"`
}

type countsRecord struct {
	Event     int `json:"event"`
	Gap       int `json:"gap"`
	Duplicate int `json:"duplicate"`
}

type trailerRecord struct {
	Type         string       `json:"type"`
	NextCursor   string       `json:"next_cursor,omitempty"`
	EndReached   bool         `json:"end_reached"`
	TailBytes    int64        `json:"tail_bytes"`
	WriterHeld   bool         `json:"writer_held"`
	Counts       countsRecord `json:"counts"`
	ScannedBytes int64        `json:"scanned_bytes"`
	DedupScope   string       `json:"dedup_scope"`
}

func (x *exporter) header(name string) error {
	h := headerRecord{Type: "header", Format: ExportFormat, Version: ExportVersion, File: name,
		Query: queryRecord{After: x.q.After, Limit: x.q.Limit, MaxBytes: x.q.MaxBytes, Request: x.q.Requests,
			Run: x.q.Runs, Tenant: x.q.Tenants, Project: x.q.Projects, Kind: x.q.Kinds}}
	if x.identified {
		h.Source = hex.EncodeToString(x.first[:])
	}
	return x.write(h)
}

func (x *exporter) event(offset int64, next string, line []byte) error {
	b := append([]byte(`{"type":"event","offset":`), strconv.FormatInt(offset, 10)...)
	b = append(append(append(b, `,"cursor":"`...), next...), `","event":`...)
	b = append(append(b, line...), "}\n"...)
	if _, err := x.out.Write(b); err != nil {
		return err
	}
	x.tr.Events++
	return nil
}

func (x *exporter) gap(offset int64, next, reason string) error {
	if err := x.write(gapRecord{Type: "gap", Offset: offset, Cursor: next, Reason: reason}); err != nil {
		return err
	}
	x.tr.Gaps++
	return nil
}

func (x *exporter) duplicate(offset int64, id string) error {
	if err := x.write(duplicateRecord{Type: "duplicate", Offset: offset, EventID: id, FirstOffset: x.seen[id].offset}); err != nil {
		return err
	}
	x.tr.Duplicates++
	return nil
}

func (x *exporter) trailer() error {
	err := x.write(trailerRecord{Type: "trailer", NextCursor: x.tr.NextCursor, EndReached: x.tr.EndReached,
		TailBytes: x.tr.TailBytes, WriterHeld: x.tr.WriterHeld, ScannedBytes: x.tr.ScannedBytes, DedupScope: "export",
		Counts: countsRecord{Event: x.tr.Events, Gap: x.tr.Gaps, Duplicate: x.tr.Duplicates}})
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

// filter is a query's filters as sets. An empty set passes every value.
type filter struct {
	requests, runs, tenants, projects map[string]bool
	kinds                             map[controlv1.EventKind]bool
}

func (f filter) match(ev *controlv1.Event) bool {
	in := func(set map[string]bool, v string) bool { return len(set) == 0 || set[v] }
	return in(f.requests, ev.GetRequestId()) && in(f.runs, ev.GetRunId()) && in(f.tenants, ev.GetTenantId()) &&
		in(f.projects, ev.GetProjectId()) && (len(f.kinds) == 0 || f.kinds[ev.GetKind()])
}

// filter checks the query and returns its filters. A kind is named as the
// contract spells it, and the unspecified one names no event anyone means.
func (q Query) filter() (filter, error) {
	switch {
	case q.Limit < 1 || q.Limit > MaxExportLimit:
		return filter{}, fmt.Errorf("%w: limit %d, want 1 to %d", ErrQuery, q.Limit, MaxExportLimit)
	case q.MaxBytes < 0:
		return filter{}, fmt.Errorf("%w: byte bound %d", ErrQuery, q.MaxBytes)
	}
	var f filter
	for _, set := range []struct {
		name   string
		values []string
		into   *map[string]bool
	}{{"request", q.Requests, &f.requests}, {"run", q.Runs, &f.runs}, {"tenant", q.Tenants, &f.tenants}, {"project", q.Projects, &f.projects}} {
		for _, v := range set.values {
			if v == "" {
				return filter{}, fmt.Errorf("%w: an empty %s", ErrQuery, set.name)
			}
			if *set.into == nil {
				*set.into = map[string]bool{}
			}
			(*set.into)[v] = true
		}
	}
	for _, name := range q.Kinds {
		n, ok := controlv1.EventKind_value[name]
		if !ok || n == int32(controlv1.EventKind_EVENT_KIND_UNSPECIFIED) {
			return filter{}, fmt.Errorf("%w: kind %s is not one the contract names", ErrQuery, strconv.Quote(name))
		}
		if f.kinds == nil {
			f.kinds = map[controlv1.EventKind]bool{}
		}
		f.kinds[controlv1.EventKind(n)] = true
	}
	return f, nil
}
