// Package reaction holds what decides that a finding may stop one run: the
// route an operator signs, the signed lift that ends a run's stops, the
// route's permit of a stop's claim and the emitter's test of a finding. It is
// pure: times and keys come in as arguments, and it reads no file and no
// clock. It holds no finding type, so a plane links it without the findings
// log or supervision. See ADR-0046.
package reaction
