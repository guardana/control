// Package gateway is the protocol-neutral pipeline of an enforcing plane: it
// takes a proposed action as an envelope, asks the kernel, applies the
// enforcement mode, holds a call for approval and writes the evidence trail,
// and answers with what to do (ADR-0013). Protocol code lives under adapters,
// which import this package; this package imports none of it.
package gateway
