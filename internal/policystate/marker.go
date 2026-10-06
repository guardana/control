package policystate

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/strictjson"
)

// MaxBundleIDs bounds how many bundle ids one directory lists.
const MaxBundleIDs = 32

// maxMarkerBytes bounds a marker read: MaxBundleIDs ids of the longest, every
// character escaped, fit under it.
const maxMarkerBytes = 72 << 10

var markerMembers = []string{"schema_version", "kind", "bundle_ids"}

// markerFileBody is the marker: the directory's kind and every bundle id Init
// ever gave a file in it, sorted. An id stays listed when its file is gone,
// so Init refuses to make that file again.
type markerFileBody struct {
	SchemaVersion string   `json:"schema_version"`
	Kind          Kind     `json:"kind"`
	BundleIDs     []string `json:"bundle_ids"`
}

// encodeMarker writes the marker, and reads it back before it returns it, as
// encodeFloorFile does.
func encodeMarker(kind Kind, ids []string) ([]byte, error) {
	body, err := encode(markerFileBody{SchemaVersion: SchemaVersion, Kind: kind, BundleIDs: ids})
	if err != nil {
		return nil, err
	}
	named, back, err := decodeMarker(body)
	if err != nil || named != kind || !slices.Equal(back, ids) {
		return nil, errors.Join(ErrUnwritable, err)
	}
	return body, nil
}

// decodeMarker reads the marker and returns the kind and the bundle ids it
// names.
func decodeMarker(raw []byte) (Kind, []string, error) {
	members, err := readObject(raw, markerMembers)
	if err != nil {
		return "", nil, err
	}
	if _, err := canon.CanonicalizeJSON(raw); err != nil {
		return "", nil, fmt.Errorf("%w: not canonical JSON", ErrMalformed)
	}
	if err := checkSchemaVersion(members["schema_version"]); err != nil {
		return "", nil, err
	}
	named, ok := strictjson.String(members["kind"])
	if !ok || checkKind(Kind(named)) != nil {
		return "", nil, fmt.Errorf("%w: kind", ErrMalformed)
	}
	ids, err := readIDs(members["bundle_ids"], "bundle_ids", MaxBundleIDs, func(id string) error {
		_, err := policy.EmptyFloor(id)
		return err
	})
	if err != nil {
		return "", nil, err
	}
	return Kind(named), ids, nil
}

// readIDs reads the list member names: one to most ids, each one check
// takes, in ascending order with none twice, so one list has one spelling.
func readIDs(raw json.RawMessage, member string, most int, check func(string) error) ([]string, error) {
	var items []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &items) != nil {
		return nil, fmt.Errorf("%w: %s is not a list", ErrMalformed, member)
	}
	switch {
	case len(items) == 0:
		return nil, fmt.Errorf("%w: %s is empty", ErrMalformed, member)
	case len(items) > most:
		return nil, fmt.Errorf("%w: %w: %d", ErrMalformed, ErrTooManyBundleIDs, len(items))
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		id, ok := strictjson.String(item)
		if !ok {
			return nil, fmt.Errorf("%w: an id in %s is not a string", ErrMalformed, member)
		}
		if err := check(id); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
		}
		if n := len(ids); n > 0 && id <= ids[n-1] {
			return nil, fmt.Errorf("%w: %s out of order or repeated", ErrMalformed, member)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
