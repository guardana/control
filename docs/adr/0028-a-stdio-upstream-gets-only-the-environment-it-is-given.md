# ADR-0028: A stdio upstream gets only the environment it is given

Status: accepted
Date: 2026-09-27

Builds on [ADR-0013](0013-mcp-interception-approvals-and-modes.md).

## Context

An upstream configured with `command` is a child process of the plane. The
plane built that command with no environment of its own, so the child
inherited the plane's whole environment. The plane reads its configuration
from variables under `GUARDANA_CONTROL_`, and an operator is told to keep
credentials there: the exporter's headers under
`GUARDANA_CONTROL_EXPORT_HEADERS_*` and the decision point's under
`GUARDANA_CONTROL_PDP_HEADERS_*`. Every stdio upstream could read them, and
every other variable of the plane's shell besides. An upstream server is a
program the operator chose to run, but it is not the plane, and nothing it
does needs the plane's credentials.

## Decision

A command upstream receives exactly:

- `PATH`, `HOME`, `LANG`, `LC_ALL`, `TMPDIR` and `USER`, each only when the
  plane has it, with the plane's value;
- the variables its `upstreams.N.env` list names, each only when the plane has
  it, with the plane's value.

The plane passes nothing else of its environment. A plane that has none of
these variables gives the child an empty environment, never its own.

The configuration refuses, at start and naming the item's key:

- a name under `GUARDANA_CONTROL_`, compared without case, since the plane's
  configuration and credentials never reach an upstream through this list;
- a name that is not a portable variable name: an ASCII letter or `_` first,
  then ASCII letters, digits and `_`. The refusal never repeats such an item,
  since a pasted `NAME=value` carries the value; an item holding `=` is told
  to list the name only;
- an `env` list on an upstream with an `endpoint`, which starts no process and
  would pass nothing.

`doctor` prints each listed name under its key, never a value, and marks a
name the environment it runs in does not have.

## Security / compatibility impact

The plane no longer hands a stdio upstream its credentials or the rest of its
environment. It does not stop the upstream taking them: the child runs as the
plane's account, and a hostile server can still read the plane's environment
on its own (on Linux from `/proc/<pid>/environ`) and the plane's
configuration file. This record narrows what the plane passes, not what the
child can reach; a server the operator does not trust runs under another
account or over HTTP.

This breaks an upstream that relied on inheriting a variable outside the
fixed six: it now starts without it. The fix is one line per variable in the
upstream's `env` list. An HTTP upstream is unchanged.

## Alternatives considered

- Pass the whole environment minus the plane's prefix. Every other variable of
  the operator's shell would still reach every upstream, and a credential of
  another program sitting in that shell is no safer than the plane's own.
- A map of names to values in the configuration file. It would put values,
  often credentials, into a file meant for version control; naming the
  variable keeps the value where the operator already keeps it.
- Allow a name under the plane's prefix when the operator lists it. The plane
  reads every such variable as its own configuration and refuses one that
  names no key, so the only names the list could pass are the plane's own
  settings and credentials.

## Consequences

An operator lists what each server needs, which is also a record of what it
gets. A server that needs its own credential reads it from a variable the
operator names. The six fixed names cover finding programs, a home directory,
the locale and a temporary directory; a server that needs more says so by
failing, and the list fixes it.

## Validation

- `TestAStdioUpstreamGetsOnlyTheEnvironmentItIsGiven` and
  `TestAStdioUpstreamOfAnEmptyEnvironmentInheritsNothing` in
  `cmd/guardana-gateway/adapter_env_test.go` start a real child through the
  transport the plane builds and read back its environment: no header
  credential, no unrelated variable, the listed one and the fixed ones with
  the plane's values, and nothing at all from a plane that has none of them.
- `TestAnEnvNameThePlaneOwnsOrNoProcessTakesIsRefused`,
  `TestAnHTTPUpstreamTakesNoEnvList`, `TestAnEnvIndexIsSpelledOnce` and
  `TestAnEnvWrittenAsOneValueSaysItTakesAList` in
  `internal/gatewayconfig/env_test.go` hold each refusal at the input where
  removing the rule changes the result, from the file and from a variable,
  and hold a pasted value out of the refusal.
- `TestDoctorPrintsAnUpstreamsEnvNamesNeverValues` holds `doctor` to names.
