package runs

import (
	"fmt"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// State is what a root run took in so far.
type State struct {
	// Untrusted is true once the run took in anything untrusted.
	Untrusted bool
	// MaxRead is the highest sensitivity read. UNSPECIFIED is unknown.
	MaxRead controlv1.Sensitivity
}

// initialState is what a root run starts with: nothing taken in.
var initialState = State{Untrusted: false, MaxRead: controlv1.Sensitivity_SENSITIVITY_PUBLIC}

// sensitivityName is the name a state file gives a sensitivity, and false for
// a value this build cannot name, which is never written.
func sensitivityName(s controlv1.Sensitivity) (string, bool) {
	switch s {
	case controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED:
		return "UNKNOWN", true
	case controlv1.Sensitivity_SENSITIVITY_PUBLIC:
		return "PUBLIC", true
	case controlv1.Sensitivity_SENSITIVITY_INTERNAL:
		return "INTERNAL", true
	case controlv1.Sensitivity_SENSITIVITY_CONFIDENTIAL:
		return "CONFIDENTIAL", true
	case controlv1.Sensitivity_SENSITIVITY_RESTRICTED:
		return "RESTRICTED", true
	case controlv1.Sensitivity_SENSITIVITY_SECRET:
		return "SECRET", true
	}
	return "", false
}

func parseSensitivity(name string) (controlv1.Sensitivity, bool) {
	for s := controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED; s <= controlv1.Sensitivity_SENSITIVITY_SECRET; s++ {
		if n, _ := sensitivityName(s); n == name {
			return s, true
		}
	}
	return 0, false
}

type stateFile struct {
	SchemaVersion string `json:"schema_version"`
	Root          string `json:"root"`
	Untrusted     bool   `json:"untrusted"`
	MaxRead       string `json:"max_read"`
}

var stateKeys = []string{"schema_version", "root", "untrusted", "max_read"}

func encodeState(root string, s State) ([]byte, error) {
	if err := checkRunID(root); err != nil {
		return nil, err
	}
	name, ok := sensitivityName(s.MaxRead)
	if !ok {
		return nil, fmt.Errorf("%w: max_read %d", ErrState, int32(s.MaxRead))
	}
	return encode(stateFile{SchemaVersion: SchemaVersion, Root: root, Untrusted: s.Untrusted, MaxRead: name})
}

// decodeState reads one state file and returns the root it names beside it.
func decodeState(raw []byte) (string, State, error) {
	f, err := fields(raw, stateKeys)
	if err != nil {
		return "", State{}, err
	}
	version, err := stringField(f, "schema_version")
	if err != nil {
		return "", State{}, err
	}
	if err := checkSchemaVersion(version); err != nil {
		return "", State{}, err
	}
	root, err := stringField(f, "root")
	if err != nil {
		return "", State{}, err
	}
	if err := checkRunID(root); err != nil {
		return "", State{}, fmt.Errorf("root: %w", err)
	}
	untrusted, err := boolField(f, "untrusted")
	if err != nil {
		return "", State{}, err
	}
	name, err := stringField(f, "max_read")
	if err != nil {
		return "", State{}, err
	}
	maxRead, ok := parseSensitivity(name)
	if !ok {
		return "", State{}, fmt.Errorf("%w: max_read %q", ErrMalformed, clip(name))
	}
	return root, State{Untrusted: untrusted, MaxRead: maxRead}, nil
}

// readState reads root's state file and holds it to its name.
func (d *dir) readState(root string) (State, error) {
	raw, err := d.readFile(root + stateSuffix)
	if err != nil {
		return State{}, err
	}
	named, s, err := decodeState(raw)
	if err != nil {
		return State{}, fmt.Errorf("state %s: %w", root, err)
	}
	if named != root {
		return State{}, fmt.Errorf("%w: %s holds root %s", ErrNameMismatch, root+stateSuffix, named)
	}
	return s, nil
}
