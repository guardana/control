# Security policy

## Supported versions

Only the latest release receives fixes. The `0.x` series makes no compatibility
promise between minor versions, and this project carries no security guarantee:
do not deploy it as a security boundary. How to check that a download is the
one the project built is in [RELEASING.md](RELEASING.md).

## Reporting a vulnerability

Use GitHub's private vulnerability reporting: open the
[Security tab](https://github.com/guardana/control/security) and choose
"Report a vulnerability". That channel is private and reaches the maintainers
directly.

Do not open a public issue, a pull request or a discussion for a suspected
vulnerability.

Include, as far as you can:

- the version, tag or commit you tested;
- how to reproduce it, ideally as a minimal case;
- what an attacker gains, and what they need in order to do it;
- anything about the configuration that matters, such as which adapter or
  policy source was in use.

## What to expect

What the project intends, rather than what it can guarantee: an acknowledgement
that a person has read your report, then a status update once the maintainers
understand the issue. There is no response-time commitment and no repair
deadline. One maintainer, no release cycle, and no paid time on this: promising
a service level here would be the kind of claim the rest of this repository
exists to avoid.

Disclosure is meant to be coordinated by agreement. Tell us if you have a date
in mind and the maintainers will say whether they can work to it. Credit goes
to reporters who want it; say so in your report.

## What is in scope

This project is not yet a security boundary. A report that a control is missing
is a roadmap item, not a vulnerability; see [ROADMAP.md](ROADMAP.md) and
[docs/status.md](docs/status.md) for what is `planned`.

What is in scope today:

- a defect in the repository itself: a build or release path that could be
  influenced by a contributor, a workflow that could leak a token, a dependency
  pinning mistake;
- a flaw in a published contract, in the canonical digest or in the
  experimental gateway that would let an approval be replayed, an action be
  misidentified, or a call run that its policy blocks;
- anything in this repository that would mislead an operator into believing a
  control exists when it does not. That is treated as a security defect here,
  not as a documentation bug.
