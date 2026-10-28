package adapterkit

import (
	"errors"
	"fmt"

	"github.com/guardana/control/internal/gateway"
	"github.com/guardana/control/pkg/contract"
)

// Unclassified is the refusal of an envelope nothing classifies, from what
// the adapter's own validation of it returned: why when validation refused
// nothing but the effect class nothing gave, and validation's refusal
// otherwise, so a call nothing classifies is held to every other rule a
// classified one is. It never joins the two, since the pipeline reads a join
// as a call refused for the other cause. A why that does not carry
// gateway.ErrUnclassified is wrapped in it.
func Unclassified(validated, why error) error {
	if validated != nil && !LacksEffect(validated) {
		return validated
	}
	switch {
	case why == nil:
		return gateway.ErrUnclassified
	case errors.Is(why, gateway.ErrUnclassified):
		return why
	}
	return fmt.Errorf("%w: %s", gateway.ErrUnclassified, why.Error())
}

// LacksEffect reports whether err is contract.Validate's refusal of an
// envelope with no effect class, which it reaches only once the bounds, the
// schema version and every field each envelope needs have passed. The refusal
// is err itself: one found inside a wrap or a join could stand beside another.
func LacksEffect(err error) bool {
	var ve *contract.ValidationError
	if !errors.As(err, &ve) || error(ve) != err { //nolint:errorlint // identity: the refusal must be err itself, not one inside it
		return false
	}
	return ve.Field == "action.effect" && errors.Is(ve.Err, contract.ErrMissingField)
}
