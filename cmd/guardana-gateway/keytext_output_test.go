package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestDoctorNamesAStrayPauseEntryWithoutKeyText: pause entries whose id and
// upstream, or whose tool, hold a key's body line are named with each
// withheld, in the lines for an upstream the configuration lacks and for a
// tool its upstream does not list.
func TestDoctorNamesAStrayPauseEntryWithoutKeyText(t *testing.T) {
	_, body := fixtureKeyText(t)
	document := pauseDocument(
		pauseEntry(body, `{"kind":"provider","provider":"`+body+`"}`),
		pauseEntry("unlisted", `{"kind":"action","action":"tool","provider":"orders","name":"`+body+`"}`))
	tr := newTree(t)
	setEnv(t, "upstreams.0.endpoint", upstream(t))
	tr.withPauseFile(t, document, time.Second)
	var stdout, stderr bytes.Buffer
	doctor(context.Background(), tr.config, &stdout, &stderr)
	out := stdout.String() + stderr.String()
	for _, want := range []string{
		`       pause entry [key text withheld] names upstream "[key text withheld]", which this configuration does not have`,
		`       pause entry unlisted names tool "[key text withheld]", which upstream "orders" does not list`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor's output does not hold %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, body[:21]) {
		t.Errorf("doctor repeated key text:\n%s", out)
	}
}

// TestRunStartsWithAPrintableLine: the line run prints once the plane is up
// withholds a bundle id holding a key's body line and quotes an evidence
// directory holding a line break, so neither forges a line.
func TestRunStartsWithAPrintableLine(t *testing.T) {
	_, body := fixtureKeyText(t)
	tr := newTree(t)
	writeBundle(t, filepath.Join(tr.dir, "policy.bundle"), strings.Replace(fixtureDocument, `"id":"gateway-fixture"`, `"id":"`+body+`"`, 1))
	evidence := filepath.Join(tr.dir, "spool\n"+forgedCheck)
	if err := os.Mkdir(evidence, 0o750); err != nil {
		t.Fatal(err)
	}
	setEnv(t, "policy.bundle_id", body)
	setEnv(t, "evidence.dir", evidence)
	setEnv(t, "upstreams.0.endpoint", upstream(t))
	setEnv(t, "listener.address", "127.0.0.1:0")
	setEnv(t, "health.address", "127.0.0.1:0")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() { done <- serve(ctx, tr.config, &stdout, &stderr) }()
	line := waitFor(t, &stdout, ": mode OBSERVE, bundle ")
	want := ": mode OBSERVE, bundle [key text withheld], evidence in " + strconv.Quote(evidence)
	if !strings.HasSuffix(line, want) {
		t.Errorf("run's first line is %q, want it to end %q", line, want)
	}
	cancel()
	<-done
	if out := stdout.String(); strings.Contains(out, body[:21]) || strings.Contains(out, "\n"+forgedCheck) {
		t.Errorf("run printed key text or a forged line:\n%s", out)
	}
}
