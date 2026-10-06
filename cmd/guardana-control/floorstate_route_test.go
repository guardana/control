package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policystate"
)

func routeInitArgs(dir string) []string {
	return []string{"policy", "state", "init", "--kind", "route", "--route-id", "refund-route", dir}
}

// TestStateInitMakesARouteFloor: init --kind route makes an owner-only route
// floor directory holding the route id's floor with no serial yet.
func TestStateInitMakesARouteFloor(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "routes")
	code, stdout, stderr := invoke(t, routeInitArgs(dir)...)
	if code != exitOK || stderr != "" {
		t.Fatalf("init: exit %d, stderr %q", code, stderr)
	}
	if want := "route_id: refund-route\nfloor: no serial\n"; stdout != want {
		t.Errorf("init printed %q, want %q", stdout, want)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the directory: %v, %v; want mode 0700", info, err)
	}
	marker, err := os.ReadFile(filepath.Join(dir, "routes.meta")) //nolint:gosec // G304: the test's own directory
	if want := `{"schema_version":"1.0","kind":"route","route_ids":["refund-route"]}` + "\n"; err != nil || string(marker) != want {
		t.Errorf("the marker holds %q, %v; want %q", marker, err, want)
	}
	f, err := policystate.ReadRoute(dir, "refund-route")
	if err != nil || f.HasSerial() || f.RouteID != "refund-route" {
		t.Errorf("ReadRoute: %+v, %v; want refund-route with no serial", f, err)
	}
}

// TestStateInitRouteRefusals: a route floor is never replaced, a route id
// is never a bundle id's flag nor the reverse, and neither kind's directory
// takes the other's floor.
func TestStateInitRouteRefusals(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "routes")
	if code, _, stderr := invoke(t, routeInitArgs(dir)...); code != exitOK {
		t.Fatalf("init: exit %d, %q", code, stderr)
	}
	digest := resetDigest
	if _, err := policystate.RaiseRoute(context.Background(), dir, "refund-route", 3, digest); err != nil {
		t.Fatal(err)
	}
	stateRefused(t, "an existing route floor", exitFail, string(policystate.ErrExists), routeInitArgs(dir)...)
	if f, err := policystate.ReadRoute(dir, "refund-route"); err != nil || f.Serial != 3 || f.Digest != digest {
		t.Errorf("after a refused init the floor is %+v, %v; want serial 3", f, err)
	}

	planeDir := filepath.Join(t.TempDir(), "floors")
	initialised(t, "plane", planeDir)
	stateRefused(t, "a route floor in a plane's directory", exitFail, string(policystate.ErrWrongKind), routeInitArgs(planeDir)...)
	stateRefused(t, "a plane's floor in a route directory", exitFail, string(policystate.ErrWrongKind), initArgs("plane", dir)...)
	stateRefused(t, "a route id no route carries", exitFail, string(policystate.ErrRouteInvalid),
		"policy", "state", "init", "--kind", "route", "--route-id", " refund-route", filepath.Join(t.TempDir(), "r"))
	stateRefused(t, "a reset of a route floor", exitFail, string(policystate.ErrKind),
		"policy", "state", "reset", "--kind", "route", "--bundle-id", "refund-route", "--empty", "--reason", "r", dir)
	for _, d := range []string{dir, planeDir} {
		entries, err := os.ReadDir(d)
		if err != nil || len(entries) != 2 {
			t.Errorf("%s holds %d entries, %v; want its marker and one floor", d, len(entries), err)
		}
	}
}

func TestStateInitRouteUsageErrors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "routes")
	for name, c := range map[string]struct {
		want string
		args []string
	}{
		"no route id": {"--route-id is missing", []string{"policy", "state", "init", "--kind", "route", dir}},
		"a bundle id for a route": {"--bundle-id names a plane's or a signer's floor",
			[]string{"policy", "state", "init", "--kind", "route", "--bundle-id", "refund-route", dir}},
		"both ids": {"--bundle-id names a plane's or a signer's floor",
			[]string{"policy", "state", "init", "--kind", "route", "--route-id", "refund-route", "--bundle-id", "x", dir}},
		"a route id for a plane": {"--route-id names a route's floor",
			[]string{"policy", "state", "init", "--kind", "plane", "--route-id", "refund-route", dir}},
		"a route id for a signer": {"--route-id names a route's floor",
			[]string{"policy", "state", "init", "--kind", "signer", "--bundle-id", "orders-policy", "--route-id", "refund-route", dir}},
		"a route id at reset": {"flag provided but not defined: -route-id",
			[]string{"policy", "state", "reset", "--kind", "plane", "--route-id", "refund-route", "--empty", "--reason", "r", dir}},
	} {
		code, stdout, stderr := invoke(t, c.args...)
		if code != exitUsage || stdout != "" || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want %d and %q", name, code, stdout, stderr, exitUsage, c.want)
		}
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a usage error made the directory")
	}
}
