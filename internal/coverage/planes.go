package coverage

import (
	"fmt"
	"slices"
	"strings"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// modeClass is what a plane in one mode does with a path it classifies. The
// zero value classifies nothing.
type modeClass uint8

const (
	modeRefused modeClass = iota
	modeDecides
	modeEnforces
)

// classOf is the plane's mode table read for coverage. SHADOW and WARN are
// declared but refused when the plane starts, so a plane configured with one
// never runs and classifies nothing.
func classOf(mode controlv1.EnforcementMode) (modeClass, error) {
	switch mode {
	case controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE,
		controlv1.EnforcementMode_ENFORCEMENT_MODE_APPROVE,
		controlv1.EnforcementMode_ENFORCEMENT_MODE_LOCKDOWN:
		return modeEnforces, nil
	case controlv1.EnforcementMode_ENFORCEMENT_MODE_OBSERVE:
		return modeDecides, nil
	case controlv1.EnforcementMode_ENFORCEMENT_MODE_SHADOW,
		controlv1.EnforcementMode_ENFORCEMENT_MODE_WARN:
		return modeRefused, nil
	}
	return modeRefused, fmt.Errorf("%w: enforcement mode %d is not one a plane runs in", ErrInput, mode)
}

func modeName(mode controlv1.EnforcementMode) string {
	return strings.TrimPrefix(mode.String(), "ENFORCEMENT_MODE_")
}

// checkPlane refuses a mode or an override effect the contract does not
// declare, which no configuration the loader accepts can hold.
func checkPlane(p Plane) error {
	if _, err := classOf(p.Mode); err != nil {
		return err
	}
	for _, o := range p.Overrides {
		v := controlv1.EffectClass(0).Descriptor().Values().ByNumber(o.Effect.Number())
		if v == nil || o.Effect == controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED {
			return fmt.Errorf("%w: plane %s: effect %d is not one the contract declares", ErrInput, printable(p.Name), o.Effect)
		}
	}
	return nil
}

// planeCoverage is the planes' state for an mcp_tool path: enforced only when
// every plane that classifies it enforces it, otherwise the weakest plane's
// state. It returns a line per plane in front of the path, and the planes
// that classify it.
func planeCoverage(planes []Plane, path Path) (State, []PlaneLine, []Plane) {
	state := NotCovered
	var lines []PlaneLine
	var by []Plane
	for _, p := range planes {
		line, classifies, ok := planeLine(p, path)
		if !ok {
			continue
		}
		lines = append(lines, line)
		if classifies {
			if len(by) == 0 || line.State < state {
				state = line.State
			}
			by = append(by, p)
		}
	}
	return state, lines, by
}

// planeLine is one plane's state for path. A plane stands in front of the
// path when it lists the path's upstream or has an override for its tool, and
// classifies it when, besides, its mode runs.
func planeLine(p Plane, path Path) (PlaneLine, bool, bool) {
	fingerprints, readUndecided := overridesFor(p, path)
	if len(fingerprints) == 0 && !slices.Contains(p.Upstreams, path.Upstream) {
		return PlaneLine{}, false, false
	}
	line := PlaneLine{Plane: p.Name}
	class, _ := classOf(p.Mode)
	head := modeName(p.Mode)
	switch {
	case class == modeRefused:
		line.Basis = head + " is refused at start by the plane, so it classifies nothing"
		return line, false, true
	case len(fingerprints) == 0:
		return unclassifiedLine(line, class, head), true, true
	case class == modeDecides:
		line.State = Decided
	case readUndecided:
		line.State = Decided
		head += " with fail_open_read: a read runs undecided while the policy is unavailable"
	default:
		line.State = Enforced
	}
	pins := "override pins fingerprint " + fingerprints[0]
	if len(fingerprints) > 1 {
		pins = "overrides pin fingerprints " + strings.Join(fingerprints, ", ")
	}
	line.Basis = head + "; " + pins + "; the live definition is not checked" + planeUnread
	return line, true, true
}

// overridesFor is the fingerprints p's overrides pin for path's tool, and
// whether one of them is a read the plane lets run undecided.
func overridesFor(p Plane, path Path) ([]string, bool) {
	var fingerprints []string
	readUndecided := false
	for _, o := range p.Overrides {
		if o.Upstream != path.Upstream || o.Tool != path.Tool {
			continue
		}
		fingerprints = append(fingerprints, printable(o.Fingerprint))
		readUndecided = readUndecided || o.Effect == controlv1.EffectClass_EFFECT_CLASS_READ && failOpenRead(p)
	}
	return fingerprints, readUndecided
}

const planeUnread = "; the plane's environment is not read"

// unclassifiedLine is a running plane's state for a tool on one of its
// upstreams that no override names: the plane blocks such a call in every
// mode but OBSERVE, which lets it run.
func unclassifiedLine(line PlaneLine, class modeClass, mode string) PlaneLine {
	const why = "; the plane lists the upstream and no override names the tool" + planeUnread
	if class == modeDecides {
		line.State, line.Basis = Decided, "an unclassified call runs in "+mode+why
		return line
	}
	line.State, line.Basis = Enforced, "an unclassified call is blocked in "+mode+why
	return line
}

// failOpenRead is the plane's effective setting: LOCKDOWN turns fail-open
// reads off whatever the configuration says.
func failOpenRead(p Plane) bool {
	return p.FailOpenRead && p.Mode != controlv1.EnforcementMode_ENFORCEMENT_MODE_LOCKDOWN
}
