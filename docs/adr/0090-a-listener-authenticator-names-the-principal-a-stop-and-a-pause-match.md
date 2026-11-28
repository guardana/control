# ADR-0090: A listener authenticator names the principal a stop and a pause match

Status: proposed
Date: 2026-11-27

Amends [ADR-0083](0083-a-finding-may-stop-an-agent-or-a-principal.md),
which matches an agent or principal stop against the identity the
listener's configuration names. Builds on
[ADR-0066](0066-each-decision-about-authority-is-a-port.md) and
[ADR-0089](0089-outside-signed-every-receiver-holds-its-input-to-the-planes-one-tenant-and-project.md).

## Context

A plane names one principal in its configuration (`listener.principal.id`).
Every call on an MCP listener is that principal's, so a principal stop or a
principal pause stops the whole plane, and two users of one plane cannot be
told apart.

The MCP adapter can take a bearer token through a listener authenticator
and read the user id from it, but no configuration wires one.

The principal an enforcement API client puts in its request is the client's
own claim; ADR-0083 refused to match a stop against it for that reason.
The go-sdk's bearer-token check refuses a token whose expiry is unset unless
told otherwise.

## Decision

- `listener.authenticator.kind` is `none` (the default) or `token_file`.
  `none` changes nothing. `token_file` requires
  `listener.authenticator.token_file`; the key is refused with any other kind,
  and the kind is refused on a stdio listener and on the enforcement API.
- The token file's format is `listener-tokens/v1alpha1`: the first line names
  the format; each later line is
  `<principal_id> sha256:<64 lowercase hex digits> <expiry as YYYY-MM-DDTHH:MM:SSZ>`.
  At most 1024 lines and 256 KiB; a principal id of at most 256 bytes under the
  contract's identifier rule; a digest given twice is refused; a principal
  may have several lines, so a token can be rotated. The file must be a regular
  file the plane's account owns, not a link, with no group or other access,
  and it is read at start only: a token is revoked by a restart or by its
  expiry. No principal type is read from it: the type stays the configured one,
  and a stop matches a principal by id alone, as ADR-0083 says.
- The listener answers 401 before any call to a request whose one
  `Authorization` header is not `Bearer` and a token whose SHA-256 one line
  names, unexpired. The plane compares every line's digest, with no early exit.
  The header, like `Cookie` and the run token, reaches no upstream.
- The authenticator is a port with three named states, unavailable (the zero
  value), refused and authenticated, declared in a package that imports no SDK;
  the gateway binary adapts it to the SDK's bearer check. `doctor` and
  `/healthz` print its kind, how many principals it names and how many requests
  it refused.
- The core knows where a call's principal came from: configured, or
  authenticated by the listener. The zero value is refused when the plane is
  built. Only the MCP listener can mark a call authenticated.
- With a `token_file` authenticator, a principal stop and a principal pause
  match the authenticated principal of the call, never the configured one,
  and a run resolves for that principal. A call with no authenticated
  principal, which an enforcement API call always is, reads an active
  principal stop as unknown: `STOP_STATE_UNAVAILABLE`, `INDETERMINATE`. With
  `none`, the configured principal is matched, as before.
- Outside `signed`, the agent and principal scopes come from the
  `reaction-route/v1alpha4` route of
  [ADR-0089](0089-outside-signed-every-receiver-holds-its-input-to-the-planes-one-tenant-and-project.md);
  `stops lift --agent` and `--principal` lift them under that route's lift key.

## Security / compatibility impact

Nothing changes for a plane that leaves the kind at `none`. A token is a
bearer secret: whoever holds it acts as that principal until it expires or the
plane restarts without it; the file holds digests only. The plane refuses a
file another account could read or change. A client that does not send a token
is refused before any call. On a plane with a principal stop active, enforcement
API calls are blocked as unknown, since none names an authenticated principal.
No wire contract under `api/proto` changes, and no reason code is added.

## Alternatives considered

- Matching the envelope principal: on the enforcement API the client chooses
  it.
- OAuth or OIDC token introspection: a network dependency in the request path
  and a library to audit; a later kind of the same port can add it.
- A principal type per token line: nothing downstream reads it, and ADR-0083
  matches by id.
- No expiry on a line: the SDK's strict default refuses it, and a token
  without an end cannot be bounded.

## Consequences

Two users of one plane can be told apart, and a stop or a pause can name one of
them. Rotating a token is adding a line and restarting. The enforcement API
stays a channel that authenticates nobody.

## Validation

A token file with group read, a link, a line without an expiry and a digest
given twice are refused at load; a request with no token, a wrong token, an
expired token and two `Authorization` headers is answered 401 and reaches no
upstream; a principal stop of one authenticated user leaves another's calls
running, with the configured principal as the negative control; an enforcement
API call under an active principal stop is `INDETERMINATE`; a zero principal
source refuses the pipeline; `scripts/ports.txt` lists the port.
