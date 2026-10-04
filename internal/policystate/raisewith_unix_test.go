//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package policystate_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
)

// waitFor is how long a call that should be blocked on the lock is given.
const waitFor = 100 * time.Millisecond

func TestRaiseWithRunsThenWhileItHoldsTheLock(t *testing.T) {
	dir, s := raised(t)
	other := open(t, dir)
	entered := make(chan policy.Floor, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	st, now := statement(t, idA, 6, d6, "11:30:00"), noon(t)
	go func() {
		_, err := s.RaiseWith(context.Background(), st, now, func(f policy.Floor) error {
			entered <- f
			<-release
			return nil
		})
		done <- err
	}()
	var given policy.Floor
	select {
	case given = <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("then was never called")
	}
	if given.Serial() != 6 {
		t.Errorf("then was given %s, want serial 6", describe(given))
	}
	if body := readFile(t, filepath.Join(dir, fileA)); !strings.Contains(body, `"serial":6,`) {
		t.Errorf("the floor file holds %s while then runs, want serial 6 written before it", body)
	}
	ctx, cancel := context.WithTimeout(t.Context(), waitFor)
	defer cancel()
	if f, err := other.Raise(ctx, statement(t, idA, 7, d7, "11:45:00"), noon(t)); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("another handle's Raise while then runs: %s, %v; want the context's deadline", describe(f), err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), waitFor)
	defer cancel()
	if rec, err := other.Record(ctx, idA); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("another handle's Record while then runs: %s, %v; want the context's deadline", describe(rec.Floor), err)
	}
	close(release)
	if err := answer(t, done); err != nil {
		t.Fatalf("RaiseWith: %v", err)
	}
	if f, err := other.Raise(t.Context(), statement(t, idA, 7, d7, "11:45:00"), noon(t)); err != nil || f.Serial() != 7 {
		t.Errorf("another handle's Raise once then returned: %s, %v", describe(f), err)
	}
}

func TestRaiseWithReturnsThensErrorAndKeepsTheRaisedFloor(t *testing.T) {
	dir, s := raised(t)
	errThen := errors.New("then refused")
	f, err := s.RaiseWith(t.Context(), statement(t, idA, 6, d6, "11:30:00"), noon(t), func(policy.Floor) error { return errThen })
	if !errors.Is(err, errThen) {
		t.Fatalf("RaiseWith: %s, %v; want then's error", describe(f), err)
	}
	if got, err := open(t, dir).Floor(t.Context(), idA); err != nil || got.Serial() != 6 {
		t.Errorf("Floor after then failed: %s, %v; want the raised serial 6", describe(got), err)
	}
}

func TestRaiseWithCallsThenWithTheFloorItLeftUnchanged(t *testing.T) {
	dir, s := raised(t)
	before := readFile(t, filepath.Join(dir, fileA))
	var given []policy.Floor
	f, err := s.RaiseWith(t.Context(), statement(t, idA, 5, d5, "11:00:00"), noon(t), func(f policy.Floor) error {
		given = append(given, f)
		return nil
	})
	if err != nil || len(given) != 1 || !given[0].Equal(f) || f.Serial() != 5 {
		t.Errorf("RaiseWith of the floor's own statement: %s, %v; then given %d floors", describe(f), err, len(given))
	}
	if after := readFile(t, filepath.Join(dir, fileA)); after != before {
		t.Errorf("the floor's own statement rewrote the file to %s", after)
	}
}

func TestRaiseWithDoesNotCallThenWhenTheRaiseFails(t *testing.T) {
	errWrite := errors.New("the write failed")
	for name, tc := range map[string]struct {
		st      func(t *testing.T) policy.Statement
		replace func(*os.Root, string, []byte, fs.FileMode) error
		want    error
	}{
		"a lower serial": {func(t *testing.T) policy.Statement { return statement(t, idA, 4, d3, "11:30:00") }, nil, policy.ErrBelowFloor},
		"an earlier renewal": {
			func(t *testing.T) policy.Statement { return statement(t, idA, 5, d5, "10:30:00") }, nil, policy.ErrBelowFloor,
		},
		"an id never initialised": {
			func(t *testing.T) policy.Statement { return statement(t, idB, 6, d6, "11:30:00") }, nil, policystate.ErrNoFloor,
		},
		"a write that fails": {
			func(t *testing.T) policy.Statement { return statement(t, idA, 6, d6, "11:30:00") },
			func(*os.Root, string, []byte, fs.FileMode) error { return errWrite }, errWrite,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, s := raised(t)
			if tc.replace != nil {
				t.Cleanup(policystate.SetReplace(tc.replace))
			}
			called := false
			f, err := s.RaiseWith(t.Context(), tc.st(t), noon(t), func(policy.Floor) error {
				called = true
				return nil
			})
			if !errors.Is(err, tc.want) {
				t.Errorf("RaiseWith: %s, %v; want %v", describe(f), err, tc.want)
			}
			if called {
				t.Error("then was called after the raise failed")
			}
		})
	}
}

func TestOpenWaitsForTheDirectoryLockUntilItsContextEnds(t *testing.T) {
	dir := initDir(t, policystate.KindPlane)
	release := holdLock(t, dir)
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), waitFor)
		defer cancel()
		s, err := policystate.OpenContext(ctx, dir, policystate.KindPlane)
		if err == nil {
			err = errors.Join(errors.New("opened while another file held the lock"), s.Close())
		}
		done <- err
	}()
	err := answer(t, done)
	release()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("OpenContext while another file holds the lock: %v; want the context's deadline", err)
	}
	s, err := policystate.OpenContext(t.Context(), dir, policystate.KindPlane)
	if err != nil {
		t.Fatalf("OpenContext once the lock was released: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Error(err)
	}
}
