package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/guardana/control/internal/trailfile"
)

// demoExport is testdata/demo.jsonl, the export of testdata/demo.trail that
// TestDemoExportIsTheExporters holds to the exporter's bytes.
func demoExport(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/demo.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const (
	demoRead    = "acme\tdemo\tTAFPANRXBE74OUOC6GPEUWIPHB\tCKP6ZFVISD6LOWS66ZAYKVCII6\tread_order\tALLOW\tRULE_ALLOW\t-\tno\t-\tcompleted\t-"
	demoRefund  = "acme\tdemo\t4KRA7XRIJZW5BOEYZ6VTI3556R\tNK5CWP266UNRKT75ZF5EN6NHZ6\trefund\tDENY\tRULE_DENY\t=\tno\t-\tblocked\t-"
	demoRead2   = "acme\tdemo\tCYFGYVUOILRYVTK7DSA35IAM7B\tNK5CWP266UNRKT75ZF5EN6NHZ6\tread_order\tALLOW\tRULE_ALLOW\t-\tno\t-\tcompleted\t-"
	demoUpdate  = "acme\tdemo\tX6UOSKZEM2VGIJQBNZWDBGDTAK\tUJ5UZ5EISEH5ER3ICMSG3MXL36\tupdate_order\tREQUIRE_APPROVAL\tAPPROVAL_REQUIRED\t-\tyes\tapproved\tcompleted\t-"
	demoExportR = "acme\tdemo\t2FT4PE3Q56YYEI22XOEUWFULBM\tZGUPQ7TZRQRODSCDE3FTKTW2JY\texport_orders\tINDETERMINATE\tRULE_ALLOW,RULE_UNDETERMINED,PDP_TIMEOUT\t=\tno\t-\tblocked\t-"
	demoRead3   = "acme\tdemo\tZV64JFN5NNHDSCTPQPVEXIC4Z4\tZGUPQ7TZRQRODSCDE3FTKTW2JY\tread_order\tALLOW\tRULE_ALLOW\t-\tno\t-\tcompleted\t-"
)

func TestDemoExport(t *testing.T) {
	check(t, reportCase{in: demoExport(t),
		rows:   []string{demoRead, demoRefund, demoRead2, demoUpdate, demoExportR, demoRead3},
		totals: "totals: requests 6, completed 4, failed 0, aborted 0, blocked 2, open 0, unknown 0; gaps 0, duplicates 0, conflicting 0, refused 0" + endReached})
}

// TestDemoExportIsTheExporters exports testdata/demo.trail with the library
// the gateway's trail export runs, named as the file in testdata/, and holds
// testdata/demo.jsonl to its bytes. demo.trail is a recording and is not
// regenerated here: the trail files the demo's dev command wrote for its
// allow, deny, approval and fail-closed scenarios, one after another in that
// order.
func TestDemoExportIsTheExporters(t *testing.T) {
	f, err := os.Open("testdata/demo.trail")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	src := trailfile.Source{Name: "demo.trail", R: f, Size: info.Size()}
	if _, err := trailfile.Export(src, trailfile.Query{Limit: trailfile.DefaultExportLimit}, &out); err != nil {
		t.Fatal(err)
	}
	got, want := strings.Split(out.String(), "\n"), strings.Split(demoExport(t), "\n")
	for i := 0; i < len(got) || i < len(want); i++ {
		switch {
		case i >= len(got) || i >= len(want):
			t.Fatalf("the exporter wrote %d lines and testdata/demo.jsonl holds %d", len(got), len(want))
		case got[i] != want[i]:
			t.Fatalf("line %d: the exporter wrote\n%s\ntestdata/demo.jsonl holds\n%s", i+1, got[i], want[i])
		}
	}
}

// TestDemoExportFormat holds the format this reader derives to the one the
// exporter wrote.
func TestDemoExportFormat(t *testing.T) {
	var h struct{ Format, Version string }
	first, _, _ := strings.Cut(demoExport(t), "\n")
	if err := json.Unmarshal([]byte(first), &h); err != nil {
		t.Fatal(err)
	}
	if h.Format != exportFormat || h.Version != "1.0" {
		t.Fatalf("the exporter wrote format %q version %q; this reader reads %q major %d", h.Format, h.Version, exportFormat, exportMajor)
	}
}

// withoutEvent is the demo export as it would read had the plane never
// delivered the event with id: its record gone and the trailer counting one
// event less.
func withoutEvent(t *testing.T, id string) string {
	t.Helper()
	var kept []string
	for _, line := range strings.Split(strings.TrimSuffix(demoExport(t), "\n"), "\n") {
		if strings.Contains(line, `"eventId":"`+id+`"`) {
			continue
		}
		kept = append(kept, strings.Replace(line, `"counts":{"event":24,`, `"counts":{"event":23,`, 1))
	}
	if len(kept) != 25 {
		t.Fatalf("event %s is not one record of the demo export", id)
	}
	return strings.Join(kept, "\n") + "\n"
}

// TestDemoExportMissingEvent reads the demo with one record the plane never
// delivered, as a quarantined record would be: the request it belonged to
// is not completed, and the report exits 1.
func TestDemoExportMissingEvent(t *testing.T) {
	notEnded := "no event ends the action"
	cases := []reportCase{
		{name: "the approval's answer", in: withoutEvent(t, "KYS2M627UE5VUQ3K5YENIRSRUJ"),
			rows: []string{demoRead, demoRefund, demoRead2,
				"acme\tdemo\tX6UOSKZEM2VGIJQBNZWDBGDTAK\tUJ5UZ5EISEH5ER3ICMSG3MXL36\tupdate_order\tREQUIRE_APPROVAL\tAPPROVAL_REQUIRED\t-\tyes\tpending\tunknown\t" +
					"event 6TQ6IH26Z6XYWW73XM3FIPBNJF follows KYS2M627UE5VUQ3K5YENIRSRUJ, which this export does not hold",
				demoExportR, demoRead3},
			totals: "totals: requests 6, completed 3, failed 0, aborted 0, blocked 2, open 0, unknown 1; gaps 0, duplicates 0, conflicting 0, refused 0" + endReached,
			code:   1, stderr: "not every request"},
		{name: "the action's end", in: withoutEvent(t, "N6C4KYP5HWIXN5IV4HX46WNENQ"),
			rows: []string{"acme\tdemo\tTAFPANRXBE74OUOC6GPEUWIPHB\tCKP6ZFVISD6LOWS66ZAYKVCII6\tread_order\tALLOW\tRULE_ALLOW\t-\tno\t-\topen\t" + notEnded,
				demoRefund, demoRead2, demoUpdate, demoExportR, demoRead3},
			totals: "totals: requests 6, completed 3, failed 0, aborted 0, blocked 2, open 1, unknown 0; gaps 0, duplicates 0, conflicting 0, refused 0" + endReached,
			code:   1, stderr: "not every request"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { check(t, tc) })
	}
}
