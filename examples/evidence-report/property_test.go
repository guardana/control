package main

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"strings"
	"testing"
)

// record is one record of a committed export, as resplit reads it.
type record struct {
	Type        string          `json:"type"`
	Offset      int64           `json:"offset"`
	Cursor      string          `json:"cursor"`
	Reason      string          `json:"reason"`
	EventID     string          `json:"event_id"`
	FirstOffset int64           `json:"first_offset"`
	Event       json.RawMessage `json:"event"`
}

// The records resplit writes, in the exporter's members and order.
type (
	gapOut struct {
		Type   string `json:"type"`
		Offset int64  `json:"offset"`
		Cursor string `json:"cursor,omitempty"`
		Reason string `json:"reason"`
	}
	duplicateOut struct {
		Type        string `json:"type"`
		Offset      int64  `json:"offset"`
		EventID     string `json:"event_id"`
		FirstOffset int64  `json:"first_offset"`
	}
	headerOut struct {
		Type    string `json:"type"`
		Format  string `json:"format"`
		Version string `json:"version"`
		File    string `json:"file"`
		Source  string `json:"source"`
		Query   struct {
			After string `json:"after,omitempty"`
			Limit int    `json:"limit"`
		} `json:"query"`
	}
	trailerOut struct {
		Type       string `json:"type"`
		NextCursor string `json:"next_cursor,omitempty"`
		EndReached bool   `json:"end_reached"`
		TailBytes  int64  `json:"tail_bytes"`
		WriterHeld bool   `json:"writer_held"`
		Counts     struct {
			Event     int `json:"event"`
			Gap       int `json:"gap"`
			Duplicate int `json:"duplicate"`
		} `json:"counts"`
		ScannedBytes int64  `json:"scanned_bytes"`
		DedupScope   string `json:"dedup_scope"`
	}
)

// records reads a committed one-shot export's records between its header
// and its trailer.
func records(t *testing.T, export string) []record {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(fixture(t, export), "\n"), "\n")
	out := make([]record, 0, len(lines)-2)
	for _, l := range lines[1 : len(lines)-1] {
		var r record
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func marshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// splitter re-frames a one-shot export of a trail as the exports that start
// at the given records would be: a line repeated after the export's first
// line of that id is a duplicate within an export, and an event again when
// its first line fell in an earlier one. A conflict is not re-framed: across
// exports it is no longer the exporter's gap, so the trails split here hold
// none. TestSplitterIsTheExporters holds it to the exporter's parts.
type splitter struct {
	trail, file, source string
	records             []record
}

// lineEnd is where the line starting at offset ends, its newline included.
func (s splitter) lineEnd(offset int64) int64 {
	return offset + int64(strings.IndexByte(s.trail[offset:], '\n')) + 1
}

// cursorAt names the line that ends at end, empty at the file's start.
func (s splitter) cursorAt(end int64) string {
	if end == 0 {
		return ""
	}
	start := strings.LastIndexByte(s.trail[:end-1], '\n') + 1
	return fmt.Sprintf("v1:%s:%d:%s", s.source, end, digest(s.trail[start:end]))
}

func (s splitter) split(t *testing.T, starts []int, limit int) []string {
	t.Helper()
	bounds := append(append([]int{0}, starts...), len(s.records))
	whole := int64(strings.LastIndexByte(s.trail, '\n') + 1)
	out := make([]string, 0, len(bounds)-1)
	var from int64
	for i := 0; i+1 < len(bounds); i++ {
		part, to := s.part(t, s.records[bounds[i]:bounds[i+1]], from, limit)
		var tr trailerOut
		tr.Type, tr.NextCursor, tr.EndReached, tr.TailBytes = "trailer", s.cursorAt(to), bounds[i+1] == len(s.records), int64(len(s.trail))-whole
		tr.ScannedBytes, tr.DedupScope = to-from, "export"
		for _, r := range part[1:] {
			switch {
			case strings.HasPrefix(r, `{"type":"event"`):
				tr.Counts.Event++
			case strings.HasPrefix(r, `{"type":"gap"`):
				tr.Counts.Gap++
			default:
				tr.Counts.Duplicate++
			}
		}
		out = append(out, strings.Join(append(part, marshal(t, tr)), "\n")+"\n")
		from = to
	}
	return out
}

// part frames one export's records from offset from, and returns where its
// last whole line ends.
func (s splitter) part(t *testing.T, rs []record, from int64, limit int) ([]string, int64) {
	t.Helper()
	var h headerOut
	h.Type, h.Format, h.Version, h.File, h.Source = "header", exportFormat, "1.0", s.file, s.source
	h.Query.After, h.Query.Limit = s.cursorAt(from), limit
	out := []string{marshal(t, h)}
	first := map[string]int64{}
	to := from
	for _, r := range rs {
		switch r.Type {
		case "gap":
			out = append(out, marshal(t, gapOut{"gap", r.Offset, r.Cursor, r.Reason}))
			if r.Cursor == "" {
				continue
			}
		case "event":
			out = append(out, fmt.Sprintf(`{"type":"event","offset":%d,"cursor":"%s","event":%s}`, r.Offset, r.Cursor, r.Event))
			first[r.EventID+string(r.Event)] = r.Offset
		default:
			line := s.trail[r.Offset : s.lineEnd(r.Offset)-1]
			if at, ok := first[r.EventID+line]; ok {
				out = append(out, marshal(t, duplicateOut{"duplicate", r.Offset, r.EventID, at}))
				break
			}
			first[r.EventID+line] = r.Offset
			out = append(out, fmt.Sprintf(`{"type":"event","offset":%d,"cursor":"%s","event":%s}`, r.Offset, s.cursorAt(s.lineEnd(r.Offset)), line))
		}
		to = s.lineEnd(r.Offset)
	}
	return out, to
}

func outageSplitter(t *testing.T) splitter {
	t.Helper()
	rs := records(t, "outage.jsonl")
	for i := range rs {
		if rs[i].Type == "event" {
			var e struct {
				EventID string `json:"eventId"`
			}
			if err := json.Unmarshal(rs[i].Event, &e); err != nil {
				t.Fatal(err)
			}
			rs[i].EventID = e.EventID
		}
		if rs[i].Type == "gap" && rs[i].Reason == "conflicting_event_id" {
			t.Fatal("outage.jsonl holds a conflict, which the splitter does not re-frame")
		}
	}
	return splitter{trail: fixture(t, "outage.trail"), file: "outage.trail", source: outageSource, records: rs}
}

// TestSplitterIsTheExporters: split where the outage's exports of four
// records stopped, the one-shot export is those exports byte for byte, the
// line repeated across two of them included.
func TestSplitterIsTheExporters(t *testing.T) {
	parts := outageSplitter(t).split(t, []int{7, 11, 15, 19}, 4)
	if len(parts) != 5 {
		t.Fatalf("%d parts, want 5", len(parts))
	}
	for i, p := range parts[1:] {
		name := fmt.Sprintf("outage-%d.jsonl", i+2)
		if want := fixture(t, name); p != want {
			t.Errorf("the splitter wrote\n%s\nthe exporter wrote %s:\n%s", p, name, want)
		}
	}
}

// TestFramedIsTheExporters: framed writes the demo's trail as the exporter
// wrote it.
func TestFramedIsTheExporters(t *testing.T) {
	lines := strings.Split(strings.TrimSuffix(fixture(t, "demo.trail"), "\n"), "\n")
	if got, want := framed("demo.trail", lines, 0, len(lines), 1000), fixture(t, "demo.jsonl"); got != want {
		t.Errorf("framed wrote\n%s\nthe exporter wrote:\n%s", got, want)
	}
}

// TestFollowAnySplit: wherever the outage's one-shot export is split, and
// whichever runs crash after the alerts were appended or before the state
// was replaced and are run again, on the same export or on a shorter one,
// the alert log is the one-shot run's. The syncs are the crash test's to
// check; here they cost time and prove nothing.
func TestFollowAnySplit(t *testing.T) {
	t.Cleanup(func() { syncFile = (*os.File).Sync })
	syncFile = func(*os.File) error { return nil }
	s := outageSplitter(t)
	one := newState(t)
	consume(t, one, fixture(t, "outage.jsonl"))
	want := alertLog(t, one)
	if strings.Count(want, "\n") != 5 {
		t.Fatalf("the one-shot run logged:\n%s\nwant its five alerts, or the property examines nothing", want)
	}
	var seen splitStats
	for seed := range uint64(24) {
		r := rand.New(rand.NewPCG(seed, 0x0e7a6e)) //nolint:gosec // G404: a seeded split the failure names, not a secret
		var starts []int
		for i := 1; i < len(s.records); i++ {
			if r.IntN(3) == 0 {
				starts = append(starts, i)
			}
		}
		dir := newState(t)
		plan := seen.follow(t, s, r, dir, starts)
		if got := alertLog(t, dir); got != want {
			t.Errorf("seed %d, %s: alerts.jsonl\n%s\nwant the one-shot run's:\n%s", seed, plan, got, want)
		}
	}
	if seen.parts < 24*4 || seen.crashed["alerts"] < 24 || seen.crashed["state"] < 24 || seen.shorter < 12 {
		t.Fatalf("%d parts, crashes %v and %d shorter reruns over 24 splits: too few to examine the property", seen.parts, seen.crashed, seen.shorter)
	}
}

// splitStats counts what the splits examined.
type splitStats struct {
	parts, shorter int
	crashed        map[string]int
}

// follow feeds the parts that start at starts to the state in dir, crashing
// some runs and running each again, now and then on a shorter part whose
// rest becomes the next. It returns what it did, for a failure to name.
func (st *splitStats) follow(t *testing.T, s splitter, r *rand.Rand, dir string, starts []int) string {
	t.Helper()
	if st.crashed == nil {
		st.crashed = map[string]int{}
	}
	var did []string
	for i := 0; i <= len(starts); i++ {
		part := s.split(t, starts, 1000)[i]
		if p := []string{"", "alerts", "state"}[r.IntN(3)]; p != "" {
			crashOnce(t, p)
			st.crashed[p]++
			consume(t, dir, part)
			did = append(did, fmt.Sprintf("crash at %s in part %d", p, i))
			if lo, hi := bounds(starts, i, len(s.records)); hi-lo > 1 && r.IntN(2) == 0 {
				starts = slices.Insert(starts, i, lo+1+r.IntN(hi-lo-1))
				part = s.split(t, starts, 1000)[i]
				st.shorter++
				did = append(did, fmt.Sprintf("rerun of part %d up to record %d", i, starts[i]))
			}
		}
		st.parts++
		if got := consume(t, dir, part); got.code == 2 {
			t.Fatalf("%q: part %d refused: %s", did, i, got.stderr)
		}
	}
	return fmt.Sprintf("parts from %v, %q", starts, did)
}

// bounds is where part i of the parts that start at starts begins and ends.
func bounds(starts []int, i, n int) (int, int) {
	all := append(append([]int{0}, starts...), n)
	return all[i], all[i+1]
}
