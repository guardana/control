// Package scenario reads an agent scenario: a strict JSON document of calls
// and operator actions, each call with the answer, the decision and the trail
// a plane must show for it. It validates every expectation against the
// registries this plane speaks before anything runs, and substitutes one
// call's output into a later call's arguments; it runs nothing itself.
package scenario
