package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// floorFiles is every file of the tree's floor directory with its bytes and
// its modification time, which doctor must leave as they are.
func (tr tree) floorFiles(t *testing.T) map[string]string {
	t.Helper()
	dir := filepath.Join(tr.dir, "floors")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name())) //nolint:gosec // G304: a file of the test's own floor directory
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = info.ModTime().Format(time.RFC3339Nano) + "\n" + string(raw)
	}
	return out
}

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestDoctorLeavesTheFloorAsItIs: a floor with no serial yet and a statement
// a start would raise it to, through every check doctor makes, the plane it
// builds included; the floor's files keep their bytes and their times.
func TestDoctorLeavesTheFloorAsItIs(t *testing.T) {
	tr := newTree(t)
	setEnv(t, "upstreams.0.endpoint", upstream(t))
	before := tr.floorFiles(t)
	time.Sleep(20 * time.Millisecond)
	var out bytes.Buffer
	if status := doctor(context.Background(), tr.config, &out, &out); status != exitOK {
		t.Fatalf("doctor did not pass:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "the floor holds no serial yet") {
		t.Errorf("the policy line does not say what the floor holds:\n%s", out.String())
	}
	if after := tr.floorFiles(t); !sameFiles(before, after) {
		t.Errorf("doctor changed the floor directory:\nbefore %q\nafter  %q", before, after)
	}
}

// raiseFloor starts a plane over the tree as it stands, which raises the
// floor to the tree's bundle, and releases it.
func (tr tree) raiseFloor(t *testing.T) {
	t.Helper()
	p, err := build(tr.load(t), slog.New(slog.DiscardHandler), time.Now(), roleServe, "")
	if err != nil {
		t.Fatalf("the plane that raises the floor did not start: %v", err)
	}
	if err := p.close(); err != nil {
		t.Fatal(err)
	}
}

// serial2 is the fixture's document at serial 2.
var serial2 = strings.Replace(fixtureDocument, `"serial":1`, `"serial":2`, 1)

// TestDoctorFailsWhatAStartWouldNotConfirm: each case fails the policy line
// naming why, and none changes the floor's files.
func TestDoctorFailsWhatAStartWouldNotConfirm(t *testing.T) {
	statement := func(tr tree) string { return filepath.Join(tr.dir, "policy.statement") }
	bundleFile := func(tr tree) string { return filepath.Join(tr.dir, "policy.bundle") }
	for _, c := range []struct {
		name    string
		arrange func(t *testing.T, tr tree)
		says    string
	}{
		{"a missing statement", func(t *testing.T, tr tree) { tr.unconfirmed(t) }, "no verified statement"},
		{"an expired statement", func(t *testing.T, tr tree) {
			writeStatement(t, statement(tr), bundleFile(tr), time.Now().Add(-11*time.Minute).Truncate(time.Second))
		}, "expired at"},
		{"a statement dated ahead", func(t *testing.T, tr tree) {
			writeStatement(t, statement(tr), bundleFile(tr), time.Now().Add(time.Hour).Truncate(time.Second))
		}, "dated after the clock"},
		{"a statement of another bundle", func(t *testing.T, tr tree) {
			other := filepath.Join(tr.dir, "other.bundle")
			writeBundle(t, other, serial2)
			writeStatement(t, statement(tr), other, time.Now().Truncate(time.Second))
		}, "names another bundle id, serial or digest"},
		{"a bundle below the floor", func(t *testing.T, tr tree) {
			writeBundle(t, bundleFile(tr), serial2)
			tr.confirm(t)
			tr.raiseFloor(t)
			writeBundle(t, bundleFile(tr), fixtureDocument)
			tr.confirm(t)
		}, "bundle serial 1, floor serial 2"},
		{"a bundle beside the floor", func(t *testing.T, tr tree) {
			tr.raiseFloor(t)
			writeBundle(t, bundleFile(tr), strings.Replace(fixtureDocument, "2026-09-20.1", "2026-09-20.1-other", 1))
			tr.confirm(t)
		}, "the floor's serial with another digest"},
	} {
		t.Run(c.name, func(t *testing.T) {
			tr := newTree(t)
			c.arrange(t, tr)
			before := tr.floorFiles(t)
			var out bytes.Buffer
			if status := doctor(context.Background(), tr.config, &out, &out); status == exitOK {
				t.Fatalf("doctor passed:\n%s", out.String())
			}
			if !strings.Contains(out.String(), "fail    policy") || !strings.Contains(out.String(), c.says) {
				t.Errorf("the policy line does not fail saying %q:\n%s", c.says, out.String())
			}
			if after := tr.floorFiles(t); !sameFiles(before, after) {
				t.Errorf("doctor changed the floor directory")
			}
		})
	}
}

// TestAMissingStatementNamesItsKeyAndItsCauseOnce: doctor's line for a
// statement file that is not there names policy.statement_file and the
// system's refusal, each once.
func TestAMissingStatementNamesItsKeyAndItsCauseOnce(t *testing.T) {
	tr := newTree(t)
	tr.unconfirmed(t)
	var out bytes.Buffer
	if status := doctor(context.Background(), tr.config, &out, &out); status == exitOK {
		t.Fatalf("doctor passed:\n%s", out.String())
	}
	want := "fail    policy        policy.statement_file: policy: no verified statement is bound to the bundle: " +
		"open <tree>/policy.statement: no such file or directory"
	if !slices.Contains(strings.Split(tr.output(out.String()), "\n"), want) {
		t.Errorf("doctor's policy line is not\n%s\nin:\n%s", want, tr.output(out.String()))
	}
}

// closeRefused is a floor directory, read through the real one, whose close
// fails.
type closeRefused struct{ floorReader }

func (c closeRefused) Close() error {
	return errors.Join(c.floorReader.Close(), errors.New("the directory handle would not close"))
}

// TestDoctorDoesNotPassAFloorDirectoryThatWouldNotClose: every other part of
// the policy check holds, and a close of the floor directory that fails is
// not ok: the run stops at the check and says why.
func TestDoctorDoesNotPassAFloorDirectoryThatWouldNotClose(t *testing.T) {
	tr := newTree(t)
	opened := openFloors
	openFloors = func(dir string) (floorReader, error) {
		r, err := opened(dir)
		if err != nil {
			return nil, err
		}
		return closeRefused{r}, nil
	}
	t.Cleanup(func() { openFloors = opened })
	var out bytes.Buffer
	if status := doctor(context.Background(), tr.config, &out, &out); status == exitOK {
		t.Fatalf("doctor passed:\n%s", out.String())
	}
	want := "unknown policy        the floor directory was read and did not close: the directory handle would not close"
	if !slices.Contains(strings.Split(out.String(), "\n"), want) {
		t.Errorf("the policy line is not\n%s\nin:\n%s", want, out.String())
	}
	if strings.Contains(out.String(), "\nok      policy ") || strings.Contains(out.String(), "\nok      evidence ") {
		t.Errorf("the check passed, or the run went on past it:\n%s", out.String())
	}

	// A check that fails on its own keeps its failure and adds the close.
	tr.unconfirmed(t)
	line := doctorLine(t, tr, "fail    policy ")
	if want := "no verified statement"; !strings.Contains(line, want) {
		t.Errorf("the policy line %q lost the failure %q", line, want)
	}
	if want := "; and the floor directory did not close: the directory handle would not close"; !strings.HasSuffix(line, want) {
		t.Errorf("the policy line %q does not end %q", line, want)
	}
}
