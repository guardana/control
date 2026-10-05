//go:build unix

package notify

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAlertsAreDeliveredInLogOrderAndInformsLeftAlone: each alert, one line
// on standard input exactly as the log carries it, in log order across
// writes; an inform finding is counted and never run; the arguments reach the
// program as given, with no shell between.
func TestAlertsAreDeliveredInLogOrderAndInformsLeftAlone(t *testing.T) {
	r := newRig(t)
	r.write(t, finding(idA, confirmed, alert), finding(idB, confirmed, inform))
	r.write(t, finding(idC, suspected, alert))
	o := r.options(true, "$(touch pwned)", "; exit 4", "`id`")
	var stderr bytes.Buffer
	o.Stderr = &stderr
	wantSummary(t, r.run(t, o), 2, 0, 0, 1)
	if got, want := r.received(t), r.logLine(t, idA, confirmed)+r.logLine(t, idC, suspected); got != want {
		t.Errorf("the program received\n%s\nwant\n%s", got, want)
	}
	if got, want := r.delivered(t), `"`+idA+`:FINDING_VERDICT_CONFIRMED"`+"\n"+`"`+idC+`:FINDING_VERDICT_SUSPECTED"`+"\n"; got != want {
		t.Errorf("delivered list = %q, want %q", got, want)
	}
	argv, err := os.ReadFile(filepath.Join(r.record, "argv"))
	mustDo(t, err)
	if want := strings.Repeat("$(touch pwned)\x00; exit 4\x00`id`\n", 2); string(argv) != want {
		t.Errorf("the program's arguments = %q, want %q", argv, want)
	}
	if got := strings.Count(stderr.String(), "helper-stderr\n"); got != 2 {
		t.Errorf("the caller's standard error got the program's %d times, want 2: %q", got, stderr.String())
	}
}

// TestADeliveredKeyIsNotDeliveredAgain: a second run delivers nothing it
// marked; the same finding with another verdict is another key.
func TestADeliveredKeyIsNotDeliveredAgain(t *testing.T) {
	r := newRig(t)
	r.write(t, finding(idA, confirmed, alert), finding(idB, suspected, alert))
	r.run(t, r.options(true))
	before := r.received(t)
	wantSummary(t, r.run(t, r.options(false)), 0, 2, 0, 0)
	if got := r.received(t); got != before {
		t.Errorf("a second run delivered %q", strings.TrimPrefix(got, before))
	}
	r.write(t, finding(idB, confirmed, alert))
	wantSummary(t, r.run(t, r.options(false)), 1, 2, 0, 0)
	if got, want := r.received(t), before+r.logLine(t, idB, confirmed); got != want {
		t.Errorf("after another verdict the program received\n%s\nwant\n%s", got, want)
	}
}

// TestACrashBetweenExitAndMarkRedeliversExactlyThatRecord: the mark of the
// second alert never lands, as in a crash after the program exited 0. The
// run stops there, and the next run delivers that record and the one after
// it, and not the first.
func TestACrashBetweenExitAndMarkRedeliversExactlyThatRecord(t *testing.T) {
	for name, ops := range map[string]fileOps{
		"the write": {write: failingWrite(2), sync: osOps.sync, openLog: osOps.openLog},
		"the sync":  {write: osOps.write, sync: failingSync(2), openLog: osOps.openLog},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t)
			r.write(t, finding(idA, confirmed, alert), finding(idB, confirmed, alert), finding(idC, confirmed, alert))
			_, err := run(t.Context(), r.options(true), ops)
			wantIs(t, "the run that crashed", err, ErrMark)
			a, b, c := r.logLine(t, idA, confirmed), r.logLine(t, idB, confirmed), r.logLine(t, idC, confirmed)
			if got := r.received(t); got != a+b {
				t.Fatalf("before the crash the program received\n%s\nwant A and B", got)
			}
			wantSummary(t, r.run(t, r.options(false)), 2, 1, 0, 0)
			if got := r.received(t); got != a+b+b+c {
				t.Errorf("the program received\n%s\nwant A, B, B, C", got)
			}
		})
	}
}

// failingWrite fails the nth write and passes the others to the file,
// writing none of the failed one.
func failingWrite(n int) func(*os.File, []byte) (int, error) {
	calls := 0
	return func(f *os.File, b []byte) (int, error) {
		if calls++; calls == n {
			return 0, errors.New("injected write failure")
		}
		return f.Write(b)
	}
}

// failingSync fails the nth sync after cutting the file back to what it held
// before the last write, as a power loss before the sync would.
func failingSync(n int) func(*os.File) error {
	calls := 0
	return func(f *os.File) error {
		if calls++; calls != n {
			return f.Sync()
		}
		info, err := f.Stat()
		if err != nil {
			return err
		}
		return errors.Join(errors.New("injected sync failure"), f.Truncate(info.Size()-int64(len(`"`+idB+`:FINDING_VERDICT_CONFIRMED"`+"\n"))))
	}
}

// TestAFailingProgramIsNotMarkedAndTheNextRecordIsTried: exit 3 on the
// first alert leaves it unmarked, the second is delivered, and the next run
// retries only the first.
func TestAFailingProgramIsNotMarkedAndTheNextRecordIsTried(t *testing.T) {
	r := newRig(t)
	r.write(t, finding(idA, confirmed, alert), finding(idB, confirmed, alert))
	s := r.run(t, r.options(true, "fail="+idA))
	wantSummary(t, s, 1, 0, 1, 0)
	if len(s.Failures) == 1 && s.Failures[0].Key != idA+":FINDING_VERDICT_CONFIRMED" {
		t.Errorf("the failure names %q", s.Failures[0].Key)
	}
	if got, want := r.delivered(t), `"`+idB+`:FINDING_VERDICT_CONFIRMED"`+"\n"; got != want {
		t.Errorf("delivered list = %q, want %q", got, want)
	}
	a, b := r.logLine(t, idA, confirmed), r.logLine(t, idB, confirmed)
	wantSummary(t, r.run(t, r.options(false)), 1, 1, 0, 0)
	if got := r.received(t); got != a+b+a {
		t.Errorf("the program received\n%s\nwant A, B, A", got)
	}
}

// TestATimeoutKillsTheProgramsGroupAndIsNotMarked: a program that hangs is
// killed at the timeout with the child it started, the next alert is still
// delivered, and the hung one is not marked.
func TestATimeoutKillsTheProgramsGroupAndIsNotMarked(t *testing.T) {
	r := newRig(t)
	r.write(t, finding(idA, confirmed, alert), finding(idB, confirmed, alert))
	o := r.options(true, "hang="+idA)
	o.Timeout = 3 * time.Second
	var stderr bytes.Buffer
	o.Stderr = &stderr
	s := r.run(t, o)
	wantSummary(t, s, 1, 0, 1, 0)
	if len(s.Failures) == 1 && !errors.Is(s.Failures[0].Err, ErrTimeout) {
		t.Errorf("the failure = %v, want ErrTimeout", s.Failures[0].Err)
	}
	if got, want := r.delivered(t), `"`+idB+`:FINDING_VERDICT_CONFIRMED"`+"\n"; got != want {
		t.Errorf("delivered list = %q, want %q", got, want)
	}
	wantChildGone(t, r)
}

// TestAProgramThatCannotStartIsNotMarked: each alert fails, none is marked,
// and a later run with a program that starts delivers both.
func TestAProgramThatCannotStartIsNotMarked(t *testing.T) {
	r := newRig(t)
	r.write(t, finding(idA, confirmed, alert), finding(idB, confirmed, alert))
	o := r.options(true)
	o.Program = filepath.Join(t.TempDir(), "absent")
	wantSummary(t, r.run(t, o), 0, 0, 2, 0)
	if got := r.delivered(t); got != "" {
		t.Errorf("delivered list = %q, want empty", got)
	}
	wantSummary(t, r.run(t, r.options(false)), 2, 0, 0, 0)
}

// TestASecondRunIsRefusedWhileOneHoldsTheState: a run started while another
// is between a delivery and its mark is ErrLocked and delivers nothing.
func TestASecondRunIsRefusedWhileOneHoldsTheState(t *testing.T) {
	r := newRig(t)
	r.write(t, finding(idA, confirmed, alert))
	var second error
	var secondSummary Summary
	ops := fileOps{
		write: func(f *os.File, b []byte) (int, error) {
			secondSummary, second = Run(t.Context(), r.options(false))
			return f.Write(b)
		},
		sync:    osOps.sync,
		openLog: osOps.openLog,
	}
	wantSummary(t, mustRun(t, r, ops), 1, 0, 0, 0)
	wantIs(t, "the second run", second, ErrLocked)
	wantSummary(t, secondSummary, 0, 0, 0, 0)
	if got := r.received(t); got != r.logLine(t, idA, confirmed) {
		t.Errorf("the program received\n%s\nwant A once", got)
	}
}

func mustRun(t *testing.T, r rig, ops fileOps) Summary {
	t.Helper()
	s, err := run(t.Context(), r.options(true), ops)
	if err != nil {
		t.Fatalf("run = %s, %v", describe(s), err)
	}
	return s
}

// TestOptionsAreRequired: each missing option is refused before the state
// is touched.
func TestOptionsAreRequired(t *testing.T) {
	r := newRig(t)
	for name, change := range map[string]func(*Options){
		"no findings": func(o *Options) { o.FindingsDir = "" },
		"no state":    func(o *Options) { o.StateDir = "" },
		"no program":  func(o *Options) { o.Program = "" },
		"no timeout":  func(o *Options) { o.Timeout = 0 },
		"negative":    func(o *Options) { o.Timeout = -time.Second },
	} {
		o := r.options(true)
		change(&o)
		_, err := Run(t.Context(), o)
		if !errors.Is(err, ErrOptions) {
			t.Errorf("%s: Run = %v, want ErrOptions", name, err)
		}
	}
	if entries, err := os.ReadDir(r.state); err != nil || len(entries) != 0 {
		t.Errorf("the state holds %d entries, %v; want none", len(entries), err)
	}
}
