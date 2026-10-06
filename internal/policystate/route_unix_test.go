//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package policystate_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guardana/control/internal/policystate"
)

// TestReadRouteTakesNoLockAndRaiseRouteWaitsForIt: while another open file
// holds the directory's lock, a read answers and a raise waits until its
// context ends, writing nothing; once the lock is free the raise is taken.
func TestReadRouteTakesNoLockAndRaiseRouteWaitsForIt(t *testing.T) {
	dir := routeRaisedTo5(t)
	release := holdLock(t, dir)
	read := make(chan error, 1)
	go func() {
		f, err := policystate.ReadRoute(dir, routeA)
		if err == nil && f.Serial != 5 {
			err = errors.New("not serial 5")
		}
		read <- err
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Errorf("ReadRoute under another's lock: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ReadRoute waited on the lock")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := policystate.RaiseRoute(ctx, dir, routeA, 6, d6); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("RaiseRoute under another's lock: %v, want context.DeadlineExceeded", err)
	}
	if got := readFile(t, filepath.Join(dir, routeFileA)); got != routeAt5 {
		t.Errorf("a raise that never held the lock wrote %s", got)
	}
	if err := policystate.InitRoute(ctx, dir, routeB); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("InitRoute under another's lock: %v, want context.DeadlineExceeded", err)
	}
	release()
	if f, err := policystate.RaiseRoute(t.Context(), dir, routeA, 6, d6); err != nil || f.Serial != 6 {
		t.Errorf("RaiseRoute once the lock is free: %s, %v", describeRoute(f), err)
	}
}

func TestARouteDirectoryIsItsOwnersAlone(t *testing.T) {
	t.Run("another owner", func(t *testing.T) {
		dir := routeRaisedTo5(t)
		restore := policystate.SetEffectiveUID(func() int { return os.Geteuid() + 1 })
		defer restore()
		if _, err := policystate.ReadRoute(dir, routeA); !errors.Is(err, policystate.ErrOwner) {
			t.Errorf("ReadRoute: %v, want ErrOwner", err)
		}
		if _, err := policystate.RaiseRoute(t.Context(), dir, routeA, 6, d6); !errors.Is(err, policystate.ErrOwner) {
			t.Errorf("RaiseRoute: %v, want ErrOwner", err)
		}
		if err := policystate.InitRoute(t.Context(), dir, routeB); !errors.Is(err, policystate.ErrOwner) {
			t.Errorf("InitRoute: %v, want ErrOwner", err)
		}
	})
	for _, path := range []string{".", routeMarker, routeFileA} {
		t.Run("the group's access to "+path, func(t *testing.T) {
			dir := routeRaisedTo5(t)
			mode := os.FileMode(0o640)
			if path == "." {
				mode = 0o750
			}
			chmod(t, filepath.Join(dir, path), mode)
			if _, err := policystate.ReadRoute(dir, routeA); !errors.Is(err, policystate.ErrPermissions) {
				t.Errorf("ReadRoute: %v, want ErrPermissions", err)
			}
			if _, err := policystate.RaiseRoute(t.Context(), dir, routeA, 6, d6); !errors.Is(err, policystate.ErrPermissions) {
				t.Errorf("RaiseRoute: %v, want ErrPermissions", err)
			}
		})
	}
	t.Run("a link at the route file", func(t *testing.T) {
		dir := routeRaisedTo5(t)
		elsewhere := filepath.Join(t.TempDir(), "route.json")
		writeFile(t, elsewhere, routeAt7)
		if err := os.Remove(filepath.Join(dir, routeFileA)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Join(dir, routeFileA)); err != nil {
			t.Fatal(err)
		}
		if f, err := policystate.ReadRoute(dir, routeA); !errors.Is(err, policystate.ErrForeignFile) {
			t.Errorf("ReadRoute through a link: %s, %v; want ErrForeignFile", describeRoute(f), err)
		}
	})
}
