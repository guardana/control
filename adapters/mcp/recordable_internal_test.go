package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// TestATooLargeRefusalClearsOnlyTheFieldItNames: an envelope refused as too
// large has the agent's string the refusal names replaced by its digest and
// keeps the others; one refused whole, which strings within their bound cannot
// cause, keeps its name and resource and loses its tags; any other refusal
// keeps them all.
func TestATooLargeRefusalClearsOnlyTheFieldItNames(t *testing.T) {
	tooLarge := fmt.Errorf("%w: 2000 bytes", contract.ErrTooLarge)
	const (
		nameDigest = "overlong:sha256:c9f8123bd2726fc2414267256101446c45647b4754b1bad58feaf205125b4c96"
		idDigest   = "overlong:sha256:a5fa777a3dc67268caea6eea038ba14177e360dba84e1db44e738dbae137e7ef"
	)
	for name, c := range map[string]struct {
		err              error
		wantName, wantID string
		wantTags         int
	}{
		"the name":        {&contract.ValidationError{Field: "action.name", Err: tooLarge}, nameDigest, "r-1", 2},
		"a tag":           {&contract.ValidationError{Field: "context.tags[1]", Err: tooLarge}, "read_file", "r-1", 0},
		"the resource":    {&contract.ValidationError{Field: "resource.id", Err: tooLarge}, "read_file", idDigest, 2},
		"the envelope":    {&contract.ValidationError{Err: tooLarge}, "read_file", "r-1", 0},
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

// TestASizeRefusalTakesOffEveryOverlongAgentString: whatever field a size
// refusal names, every name and resource the agent chose past the bound is
// replaced by its digest and every such tag goes, and one at the bound stays;
// a refusal of another kind takes none.
func TestASizeRefusalTakesOffEveryOverlongAgentString(t *testing.T) {
	at, past := strings.Repeat("a", contract.MaxStringBytes), strings.Repeat("p", contract.MaxStringBytes+1)
	sum := sha256.Sum256([]byte(past))
	pastDigest := "overlong:sha256:" + hex.EncodeToString(sum[:])
	tooLarge := &contract.ValidationError{Field: "delegation", Err: fmt.Errorf("%w: 9 hops", contract.ErrTooLarge)}
	for name, c := range map[string]struct {
		err              error
		name, id         string
		tags             []string
		wantName, wantID string
		wantTags         []string
	}{
		"past the bound": {tooLarge, past, past, []string{past, "revision"}, pastDigest, pastDigest, []string{"revision"}},
		"at the bound":   {tooLarge, at, at, []string{at, "revision"}, at, at, []string{at, "revision"}},
		"not a size":     {&contract.ValidationError{Field: "delegation", Err: contract.ErrMissingField}, past, past, []string{past}, past, past, []string{past}},
	} {
		env := &controlv1.ActionEnvelope{
			Action:   &controlv1.Action{Name: c.name},
			Resource: &controlv1.Resource{Id: c.id},
			Context:  &controlv1.RunContext{Tags: c.tags},
		}
		_ = recordable(env, c.err)
		if env.GetAction().GetName() != c.wantName || env.GetResource().GetId() != c.wantID || !slices.Equal(env.GetContext().GetTags(), c.wantTags) {
			t.Errorf("%s: name of %d bytes, resource of %d, tags %d; want %d, %d, %d", name,
				len(env.GetAction().GetName()), len(env.GetResource().GetId()), len(env.GetContext().GetTags()),
				len(c.wantName), len(c.wantID), len(c.wantTags))
		}
	}
}
