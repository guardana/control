package obligationdoc

import (
	"errors"
	"fmt"
	"slices"

	"github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/gateway"
	"github.com/guardana/control/internal/policy/rules"
)

// Applier is a part of the enforcement point that applies obligation types,
// by the name the page gives it. The parameters it reads are the catalogue's.
type Applier struct {
	Name  string
	Types []string
}

// Appliers is who applies obligations in this build, read from each one's own
// declaration: the types the gateway rewrites and the types the MCP adapter
// declares to the kernel.
func Appliers() []Applier {
	return []Applier{
		{Name: "the gateway", Types: gateway.RewritingObligations()},
		{Name: "the MCP adapter", Types: mcp.AppliedObligations()},
	}
}

// checkAppliers refuses an applier the page could not state truthfully: one
// with no name, a name a table cell cannot carry, or a type the catalogue
// does not hold, which the kernel would refuse and the page would still list.
func checkAppliers(specs []rules.ObligationSpec, appliers []Applier) error {
	for _, a := range appliers {
		if a.Name == "" {
			return errors.New("an applier with no name")
		}
		if err := checkName(a.Name); err != nil {
			return fmt.Errorf("applier name: %w", err)
		}
		for _, t := range a.Types {
			if !slices.ContainsFunc(specs, func(s rules.ObligationSpec) bool { return s.Type == t }) {
				return fmt.Errorf("%s applies %q, which the catalogue does not hold", a.Name, t)
			}
		}
	}
	return nil
}
