package reaction_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/reaction"
)

// TestARouteIDIsOneRuleEverywhere: a route document, a route floor and a
// policy floor take the same ids and refuse the same ids.
func TestARouteIDIsOneRuleEverywhere(t *testing.T) {
	zeroWidth := string(rune(0x200b))
	for _, c := range []struct {
		name string
		id   string
		want bool
	}{
		{"a word", "refunds", true},
		{"1024 bytes", strings.Repeat("r", 1024), true},
		{"1025 bytes", strings.Repeat("r", 1025), false},
		{"empty", "", false},
		{"a line break", "ref\nunds", false},
		{"a control character", "ref\x01unds", false},
		{"a space at the end", "refunds ", false},
		{"a space at the start", " refunds", false},
		{"a format character", "ref" + zeroWidth + "unds", false},
	} {
		quoted, err := json.Marshal(c.id)
		if err != nil {
			t.Fatal(err)
		}
		doc := strings.Replace(routeWith("r", "t", []string{ruleJSON("p")}), `"route_id":"r"`, `"route_id":`+string(quoted), 1)
		_, parseErr := reaction.ParseRoute([]byte(doc))
		initErr := policystate.InitRoute(t.Context(), filepath.Join(t.TempDir(), "routes"), c.id)
		_, floorErr := policy.EmptyFloor(c.id)
		if got := policy.ValidID(c.id); got != c.want {
			t.Errorf("%s: ValidID = %v, want %v", c.name, got, c.want)
		}
		if (parseErr == nil) != c.want || (!c.want && !errors.Is(parseErr, reaction.ErrRouteValue)) {
			t.Errorf("%s: ParseRoute: %v, want taken %v", c.name, parseErr, c.want)
		}
		if (initErr == nil) != c.want || (!c.want && !errors.Is(initErr, policystate.ErrRouteInvalid)) {
			t.Errorf("%s: InitRoute: %v, want taken %v", c.name, initErr, c.want)
		}
		if (floorErr == nil) != c.want {
			t.Errorf("%s: EmptyFloor: %v, want taken %v", c.name, floorErr, c.want)
		}
	}
}

// TestARouteSerialIsBoundedAsAStatementSerial: a route and a route floor take
// serial 2^53-1 and refuse 2^53.
func TestARouteSerialIsBoundedAsAStatementSerial(t *testing.T) {
	doc := withLift(routeTemplate)
	if _, err := reaction.ParseRoute([]byte(strings.Replace(doc, `"serial": 7`, `"serial": 9007199254740991`, 1))); err != nil {
		t.Errorf("ParseRoute at 2^53-1: %v", err)
	}
	_, err := reaction.ParseRoute([]byte(strings.Replace(doc, `"serial": 7`, `"serial": 9007199254740992`, 1)))
	expectOnly(t, "ParseRoute at 2^53", err, reaction.ErrRouteSerial, routeRefusals())
	dir := filepath.Join(t.TempDir(), "routes")
	if err := policystate.InitRoute(t.Context(), dir, "refunds"); err != nil {
		t.Fatal(err)
	}
	if _, err := policystate.RaiseRoute(t.Context(), dir, "refunds", 1<<53, docDigest); !errors.Is(err, policystate.ErrRouteInvalid) {
		t.Errorf("RaiseRoute at 2^53: %v, want ErrRouteInvalid", err)
	}
	if _, err := policystate.RaiseRoute(t.Context(), dir, "refunds", 1<<53-1, docDigest); err != nil {
		t.Errorf("RaiseRoute at 2^53-1: %v", err)
	}
}
