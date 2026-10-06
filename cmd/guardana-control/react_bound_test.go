package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/reaction/stoplist"
)

// TestReactAtTheBoundNamesEachFindingItCouldNotWrite: a list one line short
// of its bound takes the first stop; react goes on past the two findings the
// bound refuses, names both and exits 1.
func TestReactAtTheBoundNamesEachFindingItCouldNotWrite(t *testing.T) {
	tr := newStopTree(t)
	now := time.Now()
	body := tr.listBytes(t)
	var b bytes.Buffer
	b.Write(body)
	for i := range reaction.MaxListLines - 2 {
		line, err := reaction.Covered{FindingID: fid(100000 + i), TenantID: "acme", RunID: tr.alien,
			CreatedAt: now.Add(-time.Minute).Truncate(time.Second)}.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	writeFixture(t, filepath.Join(tr.stops, stoplist.FileName), b.String())
	log := tr.findingsDir(t, "log", finding(1, tr.open, denial, confirmed), finding(2, tr.second, denial, confirmed),
		finding(3, tr.open, outside, confirmed))
	code, stdout, stderr := tr.react(t, log, now)
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	if code != exitFail || stderr != brand.CLI+": react: 2 finding(s) the route allows were not written\n" || len(lines) != 4 {
		t.Fatalf("react answered %d: %q\n%s", code, stderr, stdout)
	}
	if want := fmt.Sprintf("stop line %d run %s finding %s rule REPEATED_DENIAL expires_at %s",
		reaction.MaxListLines, tr.open, fid(1), lineTime(now.Add(600*time.Second))); lines[0] != want {
		t.Errorf("line 1 is %q, want %q", lines[0], want)
	}
	for i, f := range []struct{ id, run string }{{fid(2), tr.second}, {fid(3), tr.open}} {
		if want := "not written: finding " + f.id + " run " + f.run + ": stopwrite: the write would take the stop list past its bounds"; !strings.HasPrefix(lines[1+i], want) {
			t.Errorf("line %d is %q, want it to start %q", 2+i, lines[1+i], want)
		}
	}
	if lines[3] != "stops 1, covered 0, already named 0, not stopping 0, not written 2" {
		t.Errorf("the summary is %q", lines[3])
	}
}
