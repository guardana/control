package recovery_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/recovery"
)

// TestRestoreRefusesAFloorBuiltUnderAnotherClasssName: the plane floor's
// directory is lost, and a restore builds it again under .floors.restoring
// beside it, the name the configuration gives the route floor. The restore
// refuses before its first write rather than remove the route floor and
// build it again from the backup's lower serial.
func TestRestoreRefusesAFloorBuiltUnderAnotherClasssName(t *testing.T) {
	p, _, _ := stopsPlane(t)
	p.raiseFloor(t, 1, d1, t0)
	cfg := string(readOrFail(t, p.path("plane.yaml")))
	writeFile(t, p.path("plane.yaml"), []byte(strings.Replace(cfg, "floor_dir: routefloor", "floor_dir: .floors.restoring", 1)))
	p.load(t)
	routeFloor := p.path(".floors.restoring")
	if err := policystate.InitRoute(t.Context(), routeFloor, "refunds"); err != nil {
		t.Fatal(err)
	}
	if _, err := policystate.RaiseRoute(t.Context(), routeFloor, "refunds", 4, d1); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "backup")
	if _, err := recovery.Backup(t.Context(), p.cfg, dir, t0); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if _, err := policystate.RaiseRoute(t.Context(), routeFloor, "refunds", 7, d2); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(p.path("floors")); err != nil {
		t.Fatal(err)
	}
	before := tree(t, p.root)
	_, err := recovery.Restore(t.Context(), p.cfg, dir, restoreAt)
	if err == nil {
		got, rerr := policystate.ReadRoute(routeFloor, "refunds")
		t.Fatalf("Restore = nil; the route floor holds serial %d (%v), and held 7", got.Serial, rerr)
	}
	if !errors.Is(err, recovery.ErrStaging) || !strings.Contains(err.Error(), "reaction.floor_dir") {
		t.Fatalf("Restore = %v, want ErrStaging naming reaction.floor_dir", err)
	}
	if after := tree(t, p.root); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatal("a refused restore changed the plane's tree")
	}
}
