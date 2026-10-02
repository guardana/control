package policystate

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
)

// readMarker reads the marker, refuses one that does not name kind, and
// returns the bundle ids it lists.
func (d *dir) readMarker(kind Kind) ([]string, error) {
	raw, err := d.readFile(markerFile, maxMarkerBytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotStateDir, err)
	}
	named, ids, err := decodeMarker(raw)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: the marker: %w", ErrNotStateDir, err)
	case named != kind:
		return nil, fmt.Errorf("%w: the marker names %s, not %s", ErrWrongKind, named, kind)
	}
	return ids, nil
}

// readListed reads the floor file of bundleID once the marker, which has to
// name kind, lists it. An id never initialised here is ErrNoFloor, whatever
// file stands under its name.
func (d *dir) readListed(kind Kind, bundleID string) (Record, error) {
	ids, err := d.readMarker(kind)
	if err != nil {
		return Record{}, err
	}
	if !slices.Contains(ids, bundleID) {
		return Record{}, fmt.Errorf("%w: %s, an id this directory never initialised", ErrNoFloor, floorName(bundleID))
	}
	return d.readRecord(bundleID)
}

// readRecord reads the floor file of bundleID and holds it to its name. A
// missing file is ErrNoFloor, naming the file.
func (d *dir) readRecord(bundleID string) (Record, error) {
	name := floorName(bundleID)
	raw, err := d.readFile(name, maxFileBytes)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Record{}, fmt.Errorf("%w: %s", ErrNoFloor, name)
	case err != nil:
		return Record{}, err
	}
	held, rec, err := decodeFloorFile(raw)
	switch {
	case err != nil:
		return Record{}, fmt.Errorf("%s: %w", name, err)
	case held != bundleID:
		return Record{}, fmt.Errorf("%w: %s", ErrNameMismatch, name)
	}
	return rec, nil
}

// writeRecord replaces the floor file rec names, whole.
func (d *dir) writeRecord(rec Record) error {
	body, err := encodeFloorFile(rec)
	if err != nil {
		return err
	}
	return replaceIn(d.root, floorName(rec.Floor.BundleID()), body, 0o600)
}
