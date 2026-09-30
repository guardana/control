# ADR-0032: The enforcement mode has no default

Status: accepted
Date: 2026-09-30

Amends [ADR-0013](0013-mcp-interception-approvals-and-modes.md) on how a plane
gets its mode.

## Context

`mode` was a required key with a default, `OBSERVE`, so the loader filled it in
and a configuration that never named a mode started a plane that enforces
nothing its policy decides. Under `OBSERVE` a material call runs whatever the
verdict, `POLICY_UNAVAILABLE` included, so a plane whose bundle was missing
ran every write. Invariant 5 lets a material effect run without its policy only
under an explicit risk setting, and the two named risk settings,
`policy.fail_open_read` and `evidence.on_unwritable`, both default closed. A
default is not explicit. The threat model admitted the default; no record
chose it.

## Decision

`mode` has no default. A configuration that sets it neither in the file nor
through its variable is refused at load, naming the key, like every other
required key without a default. `OBSERVE` stays a mode an operator may choose,
and choosing it is the explicit risk setting invariant 5 asks for: the plane
records every decision and blocks only for its own causes.

## Security / compatibility impact

A configuration without `mode` stops loading: `run`, `doctor` and `dev` exit
with the key named instead of starting a plane that stops nothing. That is a
breaking change for such a file, which the `0.x` series allows and the
changelog states. Every configuration in the repository already names its
mode. No wire contract, digest or reason code changes.

## Alternatives considered

- **Default to `ENFORCE`.** A plane started with an unfinished classification
  would block every unclassified call, and a default would still decide for the
  operator what the plane does.
- **Keep `OBSERVE` and block material calls with no policy under it.** It
  changes what `OBSERVE` means, and a plane that observes would start refusing
  writes the moment its bundle went missing, in the mode chosen to watch
  without affecting.
- **Name `mode` a risk setting and keep the default.** It keeps a silent fail
  open for whoever omits the key.

## Consequences

An operator writes one more line, and the refusal names it. The configuration
reference shows `mode` required with no default.

## Validation

`TestAConfigurationNamingNoModeIsRefused` in `internal/gatewayconfig` loads a
document without `mode` and expects the refusal naming it, then sets the mode
through its variable and expects it read. `TestFieldsSpellTheTable` pins the
key as required with no default.
