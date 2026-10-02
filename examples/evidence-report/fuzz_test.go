package main

import (
	"os"
	"strings"
	"testing"
)

// FuzzReport holds that no input reads as success unless every row it
// printed ended and the export said nothing was missing.
func FuzzReport(f *testing.F) {
	r1 := chain("r1", allowed...)
	f.Add(newExport("1.0").event(r1...).whole())
	f.Add(newExport("1.0").event(r1...).gap("malformed").duplicate("r1-2", 100).whole())
	f.Add(newExport("1.0").event(chain("r3", approved...)...).event(chain("r8", held...)...).cut())
	aborted := then(allowed[:3], step{"ACTION_FAILED", ended("x1", "BLOCKED")})
	f.Add(newExport("1.0").event(inMode(chain("r2", denied[0], denied[1], allowed[2], allowed[3]), `"ENFORCEMENT_MODE_OBSERVE"`)...).
		event(chain("r4", aborted...)...).whole())
	f.Add(strings.Replace(newExport("1.0").event(r1...).whole(), `"end_reached":true`, `"end_reached":false,"end_reached":true`, 1))
	demo, err := os.ReadFile("testdata/demo.jsonl")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(string(demo))
	f.Fuzz(func(t *testing.T, in string) {
		got := report(t, in)
		if got.code != 0 && got.code != 1 {
			t.Fatalf("exit %d on standard input alone", got.code)
		}
		if got.code != 0 {
			return
		}
		successHolds(t, got)
	})
}

// successHolds fails a run that exited 0 while saying anything was missing.
func successHolds(t *testing.T, got outcome) {
	t.Helper()
	if got.stderr != "" {
		t.Fatalf("exit 0 with diagnostics: %s", got.stderr)
	}
	rows, totals := table(t, got.stdout)
	for _, r := range rows {
		cells := strings.Split(r, "\t")
		if len(cells) != 12 {
			t.Fatalf("a row of %d cells: %q", len(cells), r)
		}
		if end := cells[10]; end != endCompleted && end != endFailed && end != endAborted && end != endBlocked {
			t.Fatalf("exit 0 with a request that ended %q: %q", end, r)
		}
	}
	if !strings.Contains(totals, "open 0, unknown 0; gaps 0,") || !strings.HasSuffix(totals, "conflicting 0, refused 0; trailer end reached") {
		t.Fatalf("exit 0 with totals %q", totals)
	}
}
