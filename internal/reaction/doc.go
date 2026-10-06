// Package reaction holds what decides that a finding may stop one run: the
// route an operator signs, the signed lift that ends a run's stops, the
// route's permit of a stop's claim, the emitter's test of a finding, and the
// judge and snapshot of a stop list. Times and keys come in as arguments: the
// package's own files import only the standard and module packages
// purity_test.go lists, and name none of the clock, deadline, input, output
// and randomness functions it refuses in them. Of internal/policykey they name
// only the key and envelope spellings it lists; what policykey itself reaches,
// file code among it, is linked and not held to that. It holds no finding
// type, so a plane links it without the findings log or supervision. See
// ADR-0046.
package reaction
