---
name: Adapter or detector
about: Support for a protocol or framework, or a new detector
labels: "kind:design"
---

Open this before writing the code, so the interface is agreed first. See
CONTRIBUTING.md.

## Which protocol or framework

<!-- Name, version, and a link to its specification or API reference. -->

## What it can observe

<!-- Which calls reach this point, and with which fields: principal,
     delegation, action, resource, arguments. Name what is missing as well as
     what is present. -->

## What it can block

<!-- Can a call be stopped before it executes, or only recorded after? Can a
     result be withheld? An adapter that can only observe is still useful, and
     has to say so rather than imply enforcement. -->

## What it cannot see

<!-- The calls that go around it. This is what decides whether the result is a
     control or a log. -->

## Fixtures that would prove it

<!-- The recorded exchanges a conformance test would replay: at least one
     allowed, one denied, and one that fails closed. -->

## Does it need a change in the core?

<!-- If yes, say which change and why translation cannot do it. Policy meaning
     lives in the core and in policy providers, never in an adapter, so this
     answer usually means the design needs another look first. -->
