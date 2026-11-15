//go:build unix

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/reaction/signedstops"
	"github.com/guardana/control/internal/reaction/stoplist"
	"github.com/guardana/control/internal/reaction/stopwrite"
	"github.com/guardana/control/internal/trust"
)

// operatorEnvelope is body signed by who under payloadType.
func operatorEnvelope(t *testing.T, who, payloadType string, body []byte) reaction.Envelope {
	t.Helper()
	sig, err := bundle.SignBytesAs(payloadType, body, signedTestKey(who))
	if err != nil {
		t.Fatal(err)
	}
	return reaction.Envelope{PayloadType: payloadType, Payload: body,
		Signatures: []reaction.Signature{{KeyID: policykey.KeyID(signedTestPublic(who)), Signature: sig}}}
}

// writeRouteOf has dave sign the fixture's route of kind at serial for the
// route file at path, naming plane where it is not empty, and writes it there.
func writeRouteOf(t *testing.T, path, kind, plane string, serial int) {
	t.Helper()
	file, err := signedstops.BindingOf(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"kind": kind, "alg": "ed25519", "keyId": policykey.KeyID(signedTestPublic("dave")), "route_id": fixtureRouteID, "serial": serial,
		"tenant_id": "acme", "project_id": "orders", "file": file,
		"rules": []any{map[string]any{"procedure_id": "refund", "version": "3", "digest": fixtureProcedure, "rule_id": fixtureRule,
			"rule_version": "1", "scope": "run", "expires_seconds": 3600}},
	}
	if plane != "" {
		doc["plane_id"] = plane
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	body, err := canon.CanonicalizeJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	out, err := reaction.MarshalRouteFile(operatorEnvelope(t, "dave", reaction.SignedRoutePayloadType, body))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, path, out)
}

// write20List replaces the list in stops with alice's 2.0 list under the
// v1alpha2 route at routePath, which the roster doc vouches for, at serial 3 with a stop of run-1, as 0.17
// wrote it, and returns that route as a carry reads it.
func write20List(t *testing.T, doc trust.Document, routePath, stops string) reaction.Route {
	t.Helper()
	plane, err := signedstops.PlaneOf("plane-a", "acme", "orders", routePath, stops, stoplist.FileName)
	if err != nil {
		t.Fatal(err)
	}
	v, err := signedstops.NewRoster(doc, plane)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(routePath) //nolint:gosec // G304: a file under the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	env, err := reaction.ParseRouteFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	from, err := reaction.VerifySignedCarrySource(env, v)
	if err != nil {
		t.Fatal(err)
	}
	body, err := reaction.HeaderBody(reaction.Header{ListID: "lst-017", RouteID: from.ID(), RouteSerial: from.Serial(), RouteDigest: from.Digest(),
		Signed: reaction.HeaderSigned{KeyID: policykey.KeyID(signedTestPublic("alice")), TenantID: "acme", ProjectID: "orders",
			File: plane.ListFile, Serial: 3}})
	if err != nil {
		t.Fatal(err)
	}
	header, err := reaction.SignedHeaderLine(operatorEnvelope(t, "alice", reaction.SignedHeaderPayloadType, body))
	if err != nil {
		t.Fatal(err)
	}
	s := stopOf("run-1", fixtureFinding, time.Now())
	s.ProjectID, s.Scope = "orders", reaction.ScopeRun
	stop, err := s.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(stops, stoplist.FileName), append(append(append(header, '\n'), stop...), '\n'))
	return from
}

// expectNoFloorRaised fails unless the stop list floor of tr holds no
// serial, and, with route, the route floor none either.
func expectNoFloorRaised(t *testing.T, tr tree, what string, route bool) {
	t.Helper()
	stops, err := policystate.ReadStops(filepath.Join(tr.dir, "stopsfloor"), "acme", "orders")
	if err != nil || stops.HasSerial() {
		t.Errorf("%s raised the stop list floor: %+v, %v", what, stops, err)
	}
	if !route {
		return
	}
	r, err := policystate.ReadRoute(filepath.Join(tr.dir, "routefloor"), fixtureRouteID)
	if err != nil || r.HasSerial() {
		t.Errorf("%s raised the route floor: %+v, %v", what, r, err)
	}
}

// TestASignedStartRefusesA017RouteAndListUntilTheyAreCarried: a start over
// dave's v1alpha2 route is refused naming route sign; over a v1alpha3 route
// of another plane, as another plane; over a v1alpha3 route and alice's 2.0
// list, naming stops init --carry; none raises a floor. Once the list is
// carried into a 3.0 list and reaction.stops names it, the start takes it:
// run-1 is stopped and the floor holds the carried head.
func TestASignedStartRefusesA017RouteAndListUntilTheyAreCarried(t *testing.T) {
	tr := signedStopsTree(t)
	routePath, stops := filepath.Join(tr.dir, "route.json"), filepath.Join(tr.dir, "stops")
	doc := writeLeveledRoster(t, filepath.Join(tr.dir, "trust", "roster.json"))
	writeRouteOf(t, routePath, reaction.SignedRouteKind, "", 1)
	from := write20List(t, doc, routePath, stops)
	if _, err := buildOneAccount(t, tr); err == nil || !strings.Contains(err.Error(), "route sign") {
		t.Fatalf("a start over a v1alpha2 route = %v, want a refusal naming route sign", err)
	}
	expectNoFloorRaised(t, tr, "a start over a v1alpha2 route", true)

	writeRouteOf(t, routePath, reaction.SignedRouteKindV3, "plane-b", 1)
	if _, err := buildOneAccount(t, tr); !errors.Is(err, reaction.ErrOtherPlane) {
		t.Fatalf("a start over plane-b's route = %v, want %v", err, reaction.ErrOtherPlane)
	}
	expectNoFloorRaised(t, tr, "a start over another plane's route", true)

	writeRouteOf(t, routePath, reaction.SignedRouteKindV3, "plane-a", 2)
	if _, err := buildOneAccount(t, tr); err == nil || !strings.Contains(err.Error(), "stops init --carry") {
		t.Fatalf("a start over a 2.0 list = %v, want a refusal naming stops init --carry", err)
	}
	expectNoFloorRaised(t, tr, "a start over a 2.0 list", false)

	carried := filepath.Join(tr.dir, "stops-018")
	if err := os.Mkdir(carried, 0o700); err != nil {
		t.Fatal(err)
	}
	route := rosterRouteOf(t, doc, routePath, carried)
	alice := stopwrite.Signer{KeyID: policykey.KeyID(signedTestPublic("alice")), Sign: func(payloadType string, body []byte) ([]byte, error) {
		return bundle.SignBytesAs(payloadType, body, signedTestKey("alice"))
	}}
	c, err := stopwrite.CarrySigned(t.Context(), stops, from, carried, route, time.Now(), alice, nil)
	if err != nil {
		t.Fatalf("the carry: %v", err)
	}
	cfg, err := os.ReadFile(tr.config)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, tr.config, []byte(strings.Replace(string(cfg), "  stops: stops\n", "  stops: stops-018\n", 1)))
	p, err := buildOneAccount(t, tr)
	if err != nil {
		t.Fatalf("a start over the carried list = %v", err)
	}
	snap := p.stops.poller.Current()
	if entries, active := snap.ActiveEntries(time.Now(), time.Now()); active != 1 || entries[0].RunID != "run-1" || entries[0].Subject != "run-1" {
		t.Fatalf("the carried list's active stops = %+v (%d), want run-1's", entries, active)
	}
	floor, err := policystate.ReadStops(filepath.Join(tr.dir, "stopsfloor"), "acme", "orders")
	if err != nil || floor.Serial != 4 || floor.Digest != c.Header.Signed.Digest {
		t.Fatalf("the stop list floor after the start = %+v, %v; want serial 4 at %s", floor, err, c.Header.Signed.Digest)
	}
}

// TestDoctorNamesA017RouteAndListAsThePlaneRefusesThem: doctor's reaction
// check fails over dave's v1alpha2 route and over alice's 2.0 list, saying
// what a start says and naming the command that upgrades each.
func TestDoctorNamesA017RouteAndListAsThePlaneRefusesThem(t *testing.T) {
	placedApart(t)
	doctorPlaneOwned = true
	t.Cleanup(func() { doctorPlaneOwned = false })

	tr := signedStopsTree(t)
	routePath := filepath.Join(tr.dir, "route.json")
	doc := writeLeveledRoster(t, filepath.Join(tr.dir, "trust", "roster.json"))
	writeRouteOf(t, routePath, reaction.SignedRouteKind, "", 1)
	write20List(t, doc, routePath, filepath.Join(tr.dir, "stops"))
	for _, c := range []struct{ name, says string }{
		{"a v1alpha2 route", reaction.ErrRouteUpgrade.Error()},
		{"a 2.0 list", reaction.ErrListUpgrade.Error()},
	} {
		if c.says == reaction.ErrListUpgrade.Error() {
			writeRouteOf(t, routePath, reaction.SignedRouteKindV3, "plane-a", 1)
		}
		status, out, stderr, line := doctorRun(t, tr, "fail    reaction ")
		if status != exitFail || !strings.HasPrefix(stderr, "doctor: reaction: ") || !strings.Contains(line, c.says) {
			t.Errorf("doctor over %s = %d, reaction line %q, stderr %q; want it to say %q:\n%s", c.name, status, line, stderr, c.says, out)
		}
	}
}
