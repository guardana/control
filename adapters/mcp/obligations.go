package mcp

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// The obligation types the adapter applies, each with a conformance test.
// The pipeline applies the rewriting ones; the kernel refuses an obligation
// outside the union of the two sets as OBLIGATION_NOT_UNDERSTOOD.
const (
	obligationReadOnly          = "read_only"
	obligationRestrictResources = "restrict_resources"
	obligationShortenTimeout    = "shorten_timeout"
	obligationDenyExternalSink  = "deny_external_sink"

	// The parameters each reads. No type has a schema in the catalogue yet,
	// so these are this adapter's reading and the page documents them.
	paramIDs    = "ids"
	paramPrefix = "prefix"
	paramMS     = "ms"
)

// AppliedObligations returns the obligation types the adapter applies to a
// call it sends, which it declares to the kernel as applicable.
func AppliedObligations() []string {
	return []string{obligationReadOnly, obligationRestrictResources, obligationShortenTimeout, obligationDenyExternalSink}
}

// applied is what the obligations of a decision demand of the send.
type applied struct {
	timeout time.Duration
}

// applyObligations checks every obligation against the call and returns
// what the send has to honour, or the obligation the call fails: a
// non-advisory one the adapter cannot satisfy for this call stops it. An
// advisory obligation that fails is skipped and the call proceeds.
func applyObligations(c *call, obligations []*controlv1.Obligation) (applied, error) {
	var out applied
	for _, o := range obligations {
		err := applyOne(c, o, &out)
		if err != nil && !o.GetAdvisory() {
			return applied{}, fmt.Errorf("%s: %w", o.GetType(), err)
		}
	}
	return out, nil
}

func applyOne(c *call, o *controlv1.Obligation, out *applied) error {
	env := c.admission.Envelope
	switch o.GetType() {
	case obligationReadOnly:
		if contract.IsMaterial(env.GetAction().GetEffect()) {
			return fmt.Errorf("the call's effect is %s, not a read", env.GetAction().GetEffect())
		}
	case obligationRestrictResources:
		id := env.GetResource().GetId()
		if id == "" || !resourceAllowed(id, o.GetParams()) {
			return errors.New("the resource is outside the allowed set")
		}
	case obligationShortenTimeout:
		return shorten(o.GetParams(), out)
	case obligationDenyExternalSink:
		if contract.IsUntrusted(env.GetDestination().GetTrustZone()) {
			return errors.New("the destination is not trusted")
		}
	default:
		return errors.New("not applied by this adapter")
	}
	return nil
}

// shorten reads ms as a positive integer and keeps the shortest timeout.
func shorten(params map[string]string, out *applied) error {
	ms, err := strconv.ParseInt(params[paramMS], 10, 64)
	if err != nil || ms <= 0 {
		return fmt.Errorf("%s is not a positive integer", paramMS)
	}
	d := time.Duration(ms) * time.Millisecond
	if out.timeout == 0 || d < out.timeout {
		out.timeout = d
	}
	return nil
}

// resourceAllowed reads the parameters as exact ids, comma-separated, and a
// literal prefix; a call passes when either matches. Neither parameter
// present allows nothing.
func resourceAllowed(id string, params map[string]string) bool {
	if ids, ok := params[paramIDs]; ok {
		for _, want := range strings.Split(ids, ",") {
			if want != "" && want == id {
				return true
			}
		}
	}
	if prefix, ok := params[paramPrefix]; ok && prefix != "" && strings.HasPrefix(id, prefix) {
		return true
	}
	return false
}
