package mcp

import (
	"errors"
	"fmt"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// TestATooLargeRefusalClearsOnlyTheFieldItNames: an envelope refused as too
// large loses the agent's string the refusal names and keeps the others; one
// refused whole loses every string the agent chose; any other refusal keeps
// them all.
func TestATooLargeRefusalClearsOnlyTheFieldItNames(t *testing.T) {
	tooLarge := fmt.Errorf("%w: 2000 bytes", contract.ErrTooLarge)
	for name, c := range map[string]struct {
		err              error
		wantName, wantID string
		wantTags         int
	}{
		"the name":        {&contract.ValidationError{Field: "action.name", Err: tooLarge}, "", "r-1", 2},
		"a tag":           {&contract.ValidationError{Field: "context.tags[1]", Err: tooLarge}, "read_file", "r-1", 0},
		"the resource":    {&contract.ValidationError{Field: "resource.id", Err: tooLarge}, "read_file", "", 2},
		"the envelope":    {&contract.ValidationError{Err: tooLarge}, "", "", 0},
		"the delegation":  {&contract.ValidationError{Field: "delegation", Err: tooLarge}, "read_file", "r-1", 2},
		"not a size":      {&contract.ValidationError{Field: "action.name", Err: contract.ErrMissingField}, "read_file", "r-1", 2},
		"no field at all": {errors.New("refused"), "read_file", "r-1", 2},
	} {
		env := &controlv1.ActionEnvelope{
			Action:   &controlv1.Action{Name: "read_file"},
			Resource: &controlv1.Resource{Id: "r-1"},
			Context:  &controlv1.RunContext{Tags: []string{"client:a/1", "revision:2025-11-25"}},
		}
		if got := recordable(env, c.err); got != c.err { //nolint:errorlint // the very refusal, not one wrapping it
			t.Errorf("%s: the refusal became %v", name, got)
		}
		if env.GetAction().GetName() != c.wantName || env.GetResource().GetId() != c.wantID || len(env.GetContext().GetTags()) != c.wantTags {
			t.Errorf("%s: name %q, resource %q, %d tag(s); want %q, %q, %d", name,
				env.GetAction().GetName(), env.GetResource().GetId(), len(env.GetContext().GetTags()), c.wantName, c.wantID, c.wantTags)
		}
	}
}
