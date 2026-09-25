// Package authzen asks an external decision point about one call over the
// OpenID AuthZEN Authorization API 1.0 and returns its answer as the kernel's
// input, core.External (ADR-0017). It is the only tree that speaks AuthZEN;
// whatever it cannot do or will not read becomes an unanswered state that
// names its cause, never an allow.
package authzen
