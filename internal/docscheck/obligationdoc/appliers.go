package obligationdoc

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/gateway"
)

// Applier is a part of the enforcement point that applies obligation types,
// by the name the page gives it, with the parameters it reads for each type.
type Applier struct {
	Name   string
	Types  []string
	Params map[string][]string
}

// Appliers is who applies obligations in this build, read from each one's own
// declaration: the types the gateway rewrites and the types the MCP adapter
// declares to the kernel, each with the parameters its applier checks.
func Appliers() []Applier {
	return []Applier{
		{Name: "the gateway", Types: gateway.RewritingObligations(), Params: gateway.ObligationParams()},
		{Name: "the MCP adapter", Types: mcp.AppliedObligations(), Params: mcp.ObligationParams()},
	}
}

// checkAppliers refuses an applier the page could not state truthfully: one
// with no name, a name a table cell cannot carry, a type the catalogue does
// not hold, which the kernel would refuse and the page would still list, or
// parameters the Parameters column cannot state.
func checkAppliers(names []string, appliers []Applier) error {
	for _, a := range appliers {
		if a.Name == "" {
			return errors.New("an applier with no name")
		}
		if err := checkName(a.Name); err != nil {
			return fmt.Errorf("applier name: %w", err)
		}
		for _, t := range a.Types {
			if !slices.Contains(names, t) {
				return fmt.Errorf("%s applies %q, which the catalogue does not hold", a.Name, t)
			}
		}
		if err := checkParams(a); err != nil {
			return err
		}
	}
	return agreeOnParams(appliers)
}

// checkParams refuses a type the applier declares with no parameter entry,
// which would read as `none` though nobody said so, an entry for a type it
// does not apply, and a parameter name a cell cannot carry or that repeats.
func checkParams(a Applier) error {
	for _, t := range a.Types {
		if _, ok := a.Params[t]; !ok {
			return fmt.Errorf("%s applies %q and declares no parameters for it", a.Name, t)
		}
	}
	for t, params := range a.Params {
		if !slices.Contains(a.Types, t) {
			return fmt.Errorf("%s declares parameters for %q, which it does not apply", a.Name, t)
		}
		for i, p := range params {
			if err := checkName(p); err != nil {
				return fmt.Errorf("%s: %s: parameter %d: %w", a.Name, t, i, err)
			}
			if strings.Contains(p, ",") {
				return fmt.Errorf("%s: %s: parameter %q holds the comma that separates parameters", a.Name, t, p)
			}
			if slices.Contains(params[:i], p) {
				return fmt.Errorf("%s: %s: parameter %q twice", a.Name, t, p)
			}
		}
	}
	return nil
}

// agreeOnParams refuses two appliers of one type that read different
// parameters: one cell cannot state both.
func agreeOnParams(appliers []Applier) error {
	for i, a := range appliers {
		for _, b := range appliers[:i] {
			for _, t := range a.Types {
				if slices.Contains(b.Types, t) && !slices.Equal(a.Params[t], b.Params[t]) {
					return fmt.Errorf("%s and %s both apply %q and read different parameters", b.Name, a.Name, t)
				}
			}
		}
	}
	return nil
}
