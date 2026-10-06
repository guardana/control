package policystate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/strictjson"
)

// routeNameDomain is hashed ahead of a route id to name its file, so a route
// id never names the file a bundle id of the same text names. Like canon's
// domain tags it ends with a newline, which no route id holds.
const routeNameDomain = "agent-route-floor-name/v1\n"

var (
	routeMarkerMembers = []string{"schema_version", "kind", "route_ids"}
	routeFileMembers   = []string{"schema_version", "route_id", "serial", "digest"}
)

// routeName is the file name of routeID's floor.
func routeName(routeID string) string {
	sum := sha256.Sum256([]byte(routeNameDomain + routeID))
	return hex.EncodeToString(sum[:]) + routeSuffix
}

// checkRouteID holds a route id to the rule a bundle id is held to.
func checkRouteID(id string) error {
	if !policy.ValidID(id) {
		return fmt.Errorf("%w: the route id", ErrRouteInvalid)
	}
	return nil
}

// check refuses a floor no route can leave: an id checkRouteID refuses, a
// serial outside 1 to policy.MaxSerial, or a digest canon does not take. The
// floor with no serial yet holds no digest either.
func (f RouteFloor) check() error {
	if err := checkRouteID(f.RouteID); err != nil {
		return err
	}
	switch {
	case f.Serial == 0 && f.Digest == "":
		return nil
	case f.Serial < 1 || f.Serial > policy.MaxSerial:
		return fmt.Errorf("%w: serial %d", ErrRouteInvalid, f.Serial)
	case !canon.ValidDigest(f.Digest):
		return fmt.Errorf("%w: the digest", ErrRouteInvalid)
	}
	return nil
}

type routeMarkerBody struct {
	SchemaVersion string   `json:"schema_version"`
	Kind          Kind     `json:"kind"`
	RouteIDs      []string `json:"route_ids"`
}

type routeFileBody struct {
	SchemaVersion string  `json:"schema_version"`
	RouteID       string  `json:"route_id"`
	Serial        *int64  `json:"serial"`
	Digest        *string `json:"digest"`
}

// encodeRouteMarker writes the route marker, and reads it back before it
// returns it, as encodeMarker does.
func encodeRouteMarker(ids []string) ([]byte, error) {
	body, err := encode(routeMarkerBody{SchemaVersion: SchemaVersion, Kind: KindRoute, RouteIDs: ids})
	if err != nil {
		return nil, err
	}
	back, err := decodeRouteMarker(body)
	if err != nil || !slices.Equal(back, ids) {
		return nil, errors.Join(ErrUnwritable, err)
	}
	return body, nil
}

// decodeRouteMarker reads the route marker and returns the route ids it
// names.
func decodeRouteMarker(raw []byte) ([]string, error) {
	members, err := readObject(raw, routeMarkerMembers)
	if err != nil {
		return nil, err
	}
	if _, err := canon.CanonicalizeJSON(raw); err != nil {
		return nil, fmt.Errorf("%w: not canonical JSON", ErrMalformed)
	}
	if err := checkSchemaVersion(members["schema_version"]); err != nil {
		return nil, err
	}
	if named, ok := strictjson.String(members["kind"]); !ok || Kind(named) != KindRoute {
		return nil, fmt.Errorf("%w: kind", ErrMalformed)
	}
	return readIDs(members["route_ids"], "route_ids", MaxRouteIDs, ErrTooManyRouteIDs, checkRouteID)
}

// encodeRouteFile writes f as one JSON line, and reads it back before it
// returns it, as encodeFloorFile does.
func encodeRouteFile(f RouteFloor) ([]byte, error) {
	if err := f.check(); err != nil {
		return nil, err
	}
	out := routeFileBody{SchemaVersion: SchemaVersion, RouteID: f.RouteID}
	if f.HasSerial() {
		out.Serial, out.Digest = &f.Serial, &f.Digest
	}
	body, err := encode(out)
	if err != nil {
		return nil, err
	}
	back, err := decodeRouteFile(body)
	if err != nil || back != f {
		return nil, errors.Join(ErrUnwritable, err)
	}
	return body, nil
}

// decodeRouteFile reads one route floor file: serial and digest both null,
// or a serial and digest check takes.
func decodeRouteFile(raw []byte) (RouteFloor, error) {
	members, err := readObject(raw, routeFileMembers)
	if err != nil {
		return RouteFloor{}, err
	}
	if _, err := canon.CanonicalizeJSON(raw); err != nil {
		return RouteFloor{}, fmt.Errorf("%w: not canonical JSON", ErrMalformed)
	}
	if err := checkSchemaVersion(members["schema_version"]); err != nil {
		return RouteFloor{}, err
	}
	id, ok := strictjson.String(members["route_id"])
	if !ok {
		return RouteFloor{}, fmt.Errorf("%w: route_id is not a string", ErrMalformed)
	}
	f := RouteFloor{RouteID: id}
	serial, digest := members["serial"], members["digest"]
	switch {
	case isNull(serial) != isNull(digest):
		return RouteFloor{}, fmt.Errorf("%w: serial and digest are not both set or both null", ErrMalformed)
	case !isNull(serial):
		if f.Serial, err = strconv.ParseInt(string(serial), 10, 64); err != nil {
			return RouteFloor{}, fmt.Errorf("%w: serial is not an integer", ErrMalformed)
		}
		if f.Digest, ok = strictjson.String(digest); !ok {
			return RouteFloor{}, fmt.Errorf("%w: digest is not a string", ErrMalformed)
		}
		if f.Serial == 0 {
			return RouteFloor{}, fmt.Errorf("%w: serial 0", ErrMalformed)
		}
	}
	if err := f.check(); err != nil {
		return RouteFloor{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	return f, nil
}
