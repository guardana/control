package evidence

import "github.com/guardana/control/internal/trailchain"

// ChainStep is one edge of the trail's state machine, as ValidateChain takes it.
type ChainStep = trailchain.Step

// ChainSteps lists every edge ValidateChain takes or refuses, for the
// reference page. Nothing that validates reads it.
func ChainSteps() []ChainStep { return trailchain.Steps() }
