## What changed, and why

<!-- One paragraph. If the title needs an "and", split the pull request. -->

## Invariant or record

<!-- Which invariant in AGENTS.md or which record in docs/adr/ this touches,
     or "none". A change to authorization semantics, a public contract, the
     layout rule or release posture carries its record in this same change. -->

## How it was tested

<!-- The command you ran and what it printed. "Should pass" is not a result.
     If something could not be verified here, write that down instead of
     leaving it out. -->

```
make quality
```

## Documentation

<!-- Which document changed with it, or "not applicable" and why. A change to
     what exists in the repository updates docs/status.md in the same pull
     request. Paste the output of `make docs-impact RANGE=<base>..HEAD`, or
     write "none because ..." when it names nothing. -->

Docs impact:

## Sign-off

- [ ] Every commit carries a Developer Certificate of Origin `Signed-off-by`
      line (`git commit -s`).
