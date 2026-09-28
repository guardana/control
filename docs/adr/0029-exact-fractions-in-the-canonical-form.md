# ADR-0029: The canonical form accepts a fraction a double holds exactly, and a tool definition any finite number

Status: accepted
Date: 2026-09-27

Amends [ADR-0005](0005-canonical-action-digest.md) on its "Floats are refused"
rule and its validation. Keeps the rule of
[ADR-0010](0010-digest-domain-separation.md): the domain tags do not change,
and no input accepted before this record hashes to different bytes.

## Context

ADR-0005 refused every number that was not an integer literal inside
+/-(2^53 - 1). It named the cost itself: an argument such as
`{"temperature": 0.7}` has no digest, so it cannot be approved, and a material
call carrying it is blocked.

The cost reached further than arguments. The MCP adapter fingerprints each
tool definition through the same canonical form. The MCP SDK decodes a schema
into float64, so a definition holding `"default": 0.7`, `"multipleOf": 0.01`
or a bound such as int64's maximum, which `json.Marshal` then writes as
`9223372036854776000`, had no fingerprint. No override could classify it, and
under ENFORCE every call to it was refused for good.

## Decision

**Arguments and the action digest: exact or refuse.** A number literal is
accepted when:

1. it reads as a finite IEEE-754 double;
2. that double is at most 2^53 - 1 in magnitude, so only the exponent form
   `e-` ever appears in the output, below 1e-6;
3. the exact decimal value of the literal equals the value of that double's
   shortest round-trip decimal. This is a comparison of decimal strings after
   removing the sign, leading and trailing zeros and the decimal point, with
   the exponent kept.

It is written as ECMAScript's `Number::toString` writes the double (RFC 8785
section 3.2.2.3), with negative zero written `0`. An integer literal takes the
path it took before, so every input accepted before this record has the same
canonical bytes and the same digest. `1.0`, `1e0` and `10e-1` hash like `1`;
`0.10` hashes like `0.1`. `Canonicalize` accepts a `json.Number` under the same
rule; float64 and float32 stay refused, because a Go float no longer says what
was written.

The domain tags stay as they are. A new tag would change the digest of every
action accepted today, which is what ADR-0010 forbids, and no accepted input
changes its bytes.

**Tool definitions: their own form.** `canon.FingerprintJSON` keeps the sorting,
escaping, UTF-8, duplicate-key and depth rules, writes every number as the
finite double nearest to it with no range bound and no exactness check, and
keeps member names that fold together, since a schema may name both `ID` and
`id`. A definition is identified by its fingerprint, never authorized by its
values. The tag `mcp-tool-fingerprint/v1\n` is unchanged, and a definition that
had a fingerprint keeps it. The digest entry points have no switch to this
form.

**What follows from it.**

- The resource id an override reads from an argument is the number's canonical
  text: `42.0` names the resource `42`, so a rule on the id 42 also matches a
  call that writes 42.0.
- `redact_fields` keeps a fraction it does not touch in its canonical form.
  `cap_amount` on a fractional amount is refused, as an obligation the plane
  cannot apply.
- A policy document stays integer-only. `internal/policy/rules` refuses a
  number with a fraction or an exponent itself, so `2.0` is not read as `2`.
- The gateway sends the agent's raw bytes upstream when no obligation rewrites
  them. The digest binds a number's value, not its spelling, its scale or
  whether it was written as an integer: a raw `1.0` and the digest's `1` are one
  value to it. An upstream that reads `1.0` as a float and `1` as an integer
  sees a difference the digest does not bind.

## Security / compatibility impact

Nothing that was accepted changes its bytes: the goldens in `testdata/digest/`
are pinned by sha256, and a fixture of `refund_prod` with its integers written
`1250.0`, `2e0` and `10e-1` has `refund_prod`'s digest. Input that was refused
may now be accepted; that is the only change, and it invalidates no stored
approval.

The exactness rule is what keeps two implementations in agreement. Any reader
that parses a literal into a double and writes it back as RFC 8785 says gets
the canonical text, so the numeric value a digest covers is the value a
JavaScript, Python or Go consumer reads. The digest binds that value only: not
the literal's spelling, its scale (`0.10` against `0.1`) or whether a reader
takes it as an integer or a float. A literal that rounds, such as
`0.30000000000000001`, is refused rather than covered as something it does
not say.

A fingerprint can now collide where a definition differs only in digits a
double does not hold, such as two bounds past 2^53 that round to one double.
The SDK has already decoded the definition through float64 by the time it is
fingerprinted, so the adapter never saw the digits that differ.

## Alternatives considered

- **Keep refusing fractions.** Leaves every tool with a fractional default
  unclassifiable and every call carrying a fraction unapprovable.
- **Accept any finite double in the digest, rounding like the fingerprint.**
  Gives `0.30000000000000001` and `0.3` one digest: an approval would cover a
  value nobody was shown.
- **Keep the literal's own spelling.** Two producers writing one value
  differently would compute two digests, and every JSON library normalizes
  numbers differently.
- **A new domain tag.** Changes the digest of every action accepted today and
  invalidates every stored approval, against ADR-0010, for no gain, since no
  accepted input changes its bytes.
- **Arbitrary precision with `math/big`.** Not on the dependency rule's list
  for the canonical form's tree, and not needed: the comparison is between two
  decimal strings.

## Consequences

A tool with fractions in its schema can be classified, and a call carrying a
fraction can be decided, approved and recorded. A second implementation needs
the shortest round-trip formatter of its language and a string comparison;
`testdata/digest/numbers.json` lists literals it must accept and write, and
literals it must refuse.

## Validation

`go test ./internal/canon/` runs `numbers.json`, the `fractions` and
`integral_spelled` fixtures, a sha256 pin of every golden file, the Appendix B
vectors of RFC 8785 and a sample of numbers written by an ECMAScript engine,
through both forms. `FuzzSafeIntegers` checks that every integer inside the
JSON-safe range is written as its digits, and `FuzzCanonicalizeJSON` that the
definition form gives the same bytes as the action form for every document the
action form accepts. `go test ./adapters/mcp/` pins the fingerprint of a
definition with fractions and of one with integers only, and denies a call
naming the resource `42` written as `42.0`. `go test ./internal/gateway/` and
`go test ./internal/policy/rules/` hold the rewrite and the policy format to
the rules above.
