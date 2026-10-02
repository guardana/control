package policystate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/strictjson"
	"github.com/guardana/control/pkg/contract"
)

// SchemaVersion is the version this build writes into every file. A reader
// takes any minor of major 1 and refuses another major.
const SchemaVersion = "1.0"

// MaxReasonBytes bounds a reset's reason.
const MaxReasonBytes = 256

// floorName is the file name of bundleID's floor: the hex SHA-256 of the id,
// so any id a bundle can carry names one file and no id names a path.
func floorName(bundleID string) string {
	sum := sha256.Sum256([]byte(bundleID))
	return hex.EncodeToString(sum[:]) + floorSuffix
}

var (
	floorMembers = []string{"schema_version", "bundle_id", "serial", "digest", "issued_at", "latest_issued_at", "reset_reason", "reset_from"}
	valueMembers = []string{"serial", "digest", "issued_at", "latest_issued_at"}
)

// floorValue is a floor's members as a file holds them, all null while it
// holds no serial yet.
type floorValue struct {
	Serial         *int64  `json:"serial"`
	Digest         *string `json:"digest"`
	IssuedAt       *string `json:"issued_at"`
	LatestIssuedAt *string `json:"latest_issued_at"`
}

type floorFile struct {
	SchemaVersion string `json:"schema_version"`
	BundleID      string `json:"bundle_id"`
	floorValue
	ResetReason *string     `json:"reset_reason"`
	ResetFrom   *floorValue `json:"reset_from"`
}

func valueOf(f policy.Floor) floorValue {
	if !f.HasSerial() {
		return floorValue{}
	}
	serial, digest := f.Serial(), f.Digest()
	issued, latest := policy.FormatIssuedAt(f.IssuedAt()), policy.FormatIssuedAt(f.LatestIssuedAt())
	return floorValue{Serial: &serial, Digest: &digest, IssuedAt: &issued, LatestIssuedAt: &latest}
}

// encodeFloorFile writes rec as one JSON line, and reads the line back before
// it returns it: a file the reader refuses is a floor nothing can read or
// repair, so a record that does not read back as itself is ErrUnwritable.
func encodeFloorFile(rec Record) ([]byte, error) {
	body, err := encodeRecord(rec)
	if err != nil {
		return nil, err
	}
	id, back, err := decodeFloorFile(body)
	if err != nil || id != rec.Floor.BundleID() || !sameRecord(rec, back) {
		return nil, errors.Join(ErrUnwritable, err)
	}
	return body, nil
}

func encodeRecord(rec Record) ([]byte, error) {
	id := rec.Floor.BundleID()
	if id == "" {
		return nil, policy.ErrFloorInvalid
	}
	out := floorFile{SchemaVersion: SchemaVersion, BundleID: id, floorValue: valueOf(rec.Floor)}
	if rec.Reset != nil {
		if err := checkReason(rec.Reset.Reason); err != nil {
			return nil, err
		}
		if rec.Reset.From.BundleID() != id {
			return nil, fmt.Errorf("%w: the reset's prior floor is another bundle id's", policy.ErrFloorBundle)
		}
		reason, from := rec.Reset.Reason, valueOf(rec.Reset.From)
		out.ResetReason = &reason
		if !rec.Reset.FileMissing {
			out.ResetFrom = &from
		}
	}
	return encode(out)
}

// decodeFloorFile reads one floor file and returns the bundle id it holds
// beside its record.
func decodeFloorFile(raw []byte) (string, Record, error) {
	members, err := readObject(raw, floorMembers)
	if err != nil {
		return "", Record{}, err
	}
	// The canonical reader refuses what strictjson passes: invalid UTF-8, an
	// unpaired surrogate, a number outside the JSON-safe range.
	if _, err := canon.CanonicalizeJSON(raw); err != nil {
		return "", Record{}, fmt.Errorf("%w: not canonical JSON", ErrMalformed)
	}
	if err := checkSchemaVersion(members["schema_version"]); err != nil {
		return "", Record{}, err
	}
	id, ok := strictjson.String(members["bundle_id"])
	if !ok {
		return "", Record{}, fmt.Errorf("%w: bundle_id is not a string", ErrMalformed)
	}
	floor, err := readValue(id, members)
	if err != nil {
		return "", Record{}, err
	}
	reset, err := readReset(id, members["reset_reason"], members["reset_from"])
	if err != nil {
		return "", Record{}, err
	}
	return id, Record{Floor: floor, Reset: reset}, nil
}

// readObject reads raw as one strict JSON object holding each of names once
// and nothing else.
func readObject(raw []byte, names []string) (strictjson.Object, error) {
	o, err := strictjson.ReadObject(raw)
	switch {
	case errors.Is(err, strictjson.ErrRepeated):
		return nil, fmt.Errorf("%w: %w", ErrDuplicateField, err)
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if err := o.Only(names...); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnknownField, err)
	}
	if err := o.Require(names...); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMissingField, err)
	}
	return o, nil
}

// readValue reads a floor of id from the four members valueMembers names:
// all null, or a floor policy.NewFloor takes.
func readValue(id string, m strictjson.Object) (policy.Floor, error) {
	if isNull(m["serial"]) {
		for _, name := range valueMembers[1:] {
			if !isNull(m[name]) {
				return policy.Floor{}, fmt.Errorf("%w: %s with no serial", ErrMalformed, name)
			}
		}
		f, err := policy.EmptyFloor(id)
		if err != nil {
			return policy.Floor{}, fmt.Errorf("%w: %w", ErrMalformed, err)
		}
		return f, nil
	}
	serial, err := strconv.ParseInt(string(m["serial"]), 10, 64)
	if err != nil {
		return policy.Floor{}, fmt.Errorf("%w: serial is not an integer", ErrMalformed)
	}
	digest, ok := strictjson.String(m["digest"])
	if !ok {
		return policy.Floor{}, fmt.Errorf("%w: digest is not a string", ErrMalformed)
	}
	issued, err := readTime(m, "issued_at")
	if err != nil {
		return policy.Floor{}, err
	}
	latest, err := readTime(m, "latest_issued_at")
	if err != nil {
		return policy.Floor{}, err
	}
	f, err := policy.NewFloor(id, serial, digest, issued, latest)
	if err != nil {
		return policy.Floor{}, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	return f, nil
}

func readTime(m strictjson.Object, name string) (time.Time, error) {
	s, ok := strictjson.String(m[name])
	if !ok {
		return time.Time{}, fmt.Errorf("%w: %s is not a string", ErrMalformed, name)
	}
	t, err := policy.ParseIssuedAt(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %s: %w", ErrMalformed, name, err)
	}
	return t, nil
}

// readReset reads reset_reason and reset_from: both null; a reason and the
// floor of id it replaced; or a reason and a null reset_from, for a reset
// that found the file missing.
func readReset(id string, reason, from json.RawMessage) (*ResetNote, error) {
	if isNull(reason) && isNull(from) {
		return nil, nil
	}
	text, ok := strictjson.String(reason)
	if !ok {
		return nil, fmt.Errorf("%w: reset_from with no reset_reason", ErrMalformed)
	}
	if err := checkReason(text); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	if isNull(from) {
		empty, err := policy.EmptyFloor(id)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
		}
		return &ResetNote{Reason: text, From: empty, FileMissing: true}, nil
	}
	if len(from) == 0 || from[0] != '{' {
		return nil, fmt.Errorf("%w: reset_from is not an object", ErrMalformed)
	}
	members, err := readObject(from, valueMembers)
	if err != nil {
		return nil, fmt.Errorf("reset_from: %w", err)
	}
	prior, err := readValue(id, members)
	if err != nil {
		return nil, fmt.Errorf("reset_from: %w", err)
	}
	return &ResetNote{Reason: text, From: prior}, nil
}

func isNull(raw json.RawMessage) bool { return string(raw) == "null" }

// checkReason holds a reset's reason to a line a log can carry as it is:
// not empty, at most MaxReasonBytes, and no character an identifier refuses.
func checkReason(reason string) error {
	switch {
	case reason == "":
		return fmt.Errorf("%w: empty", ErrReason)
	case len(reason) > MaxReasonBytes:
		return fmt.Errorf("%w: %d bytes, bound %d", ErrReason, len(reason), MaxReasonBytes)
	}
	if err := contract.CheckIdentifier(reason); err != nil {
		return fmt.Errorf("%w: %w", ErrReason, err)
	}
	return nil
}

// checkSchemaVersion refuses a version this build does not read. The major
// decides: a later minor may add a member, and the unknown-member rule
// refuses that file too, so nothing is dropped in silence.
func checkSchemaVersion(raw json.RawMessage) error {
	v, ok := strictjson.String(raw)
	if !ok {
		return fmt.Errorf("%w: schema_version is not a string", ErrSchemaVersion)
	}
	major, minor, ok := strings.Cut(v, ".")
	if !ok || major != "1" || minor == "" || strings.Trim(minor, "0123456789") != "" {
		return fmt.Errorf("%w: %q", ErrSchemaVersion, clip(v))
	}
	return nil
}

// encode writes v as one JSON line. HTML escaping is off: it would only make
// a member longer than the bound it was checked against.
func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// sameRecord reports whether a and b are the same record, floors compared as
// Floor.Equal compares them.
func sameRecord(a, b Record) bool {
	if !a.Floor.Equal(b.Floor) || (a.Reset == nil) != (b.Reset == nil) {
		return false
	}
	return a.Reset == nil || (a.Reset.Reason == b.Reset.Reason && a.Reset.FileMissing == b.Reset.FileMissing && a.Reset.From.Equal(b.Reset.From))
}
