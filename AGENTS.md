# Agent instructions

Repository-specific rules for automated and agent-assisted contributions.
`CONTRIBUTING.md` remains the source of truth for everyone; these notes make the
parts that are easy to get wrong explicit.

## Before opening a pull request

- Run `scripts/check.sh`. It covers formatting, the race-enabled test suite,
  vet, shell scripts, protocol and release consistency, and a reproducible
  package build.
- In the pull request body, state what you tested and what you did not. "Not
  tested on hardware" is a valid answer. Do not describe a change as tested
  when it is not.

## Never include identifiers or credentials

Tests, fixtures, logs, commit messages, issues, and pull request bodies must not
contain a real MAC address, hostname, client name, household IP address, router
dump, bearer token, or wireless credential. Use reserved values:

- IPv4 and IPv6 from RFC 5737 and RFC 3849, for example `192.0.2.10` and
  `2001:db8::1`;
- locally administered MAC addresses such as `02:00:00:00:00:01`;
- placeholder tokens built from obvious patterns rather than copied values.

## Boundaries the implementation keeps

- Observations, connections, and derived presence stay separate. A provider
  publishes normalized observations and never decides presence.
- Public operations stay read-only. The agent exposes no shell, generic ubus
  call, UCI mutation, or router control, and a change must not add one.
- Provider-specific payloads stay behind the normalized observation layer.
- The module has no external dependencies on purpose. Adding one requires the
  maintainer's approval and the reason in the pull request body.
- Every queue, map, buffer, and goroutine carries an explicit bound and a test
  that exercises the limit. An unbounded one is a defect, not a tradeoff.

## Packaging and credentials

- The package adds no firewall rule, and it must not gain one. Reachability is
  the operator's decision.
- The bearer token and agent identity files stay root-only with mode `0600`.
  The init script and `postinst` keep those permissions on every path.

## Evidence and claims

- A compatibility claim names the router model, firmware version, and package
  architecture, or says the change is not tested on hardware. Do not widen a
  support claim without the checklist in `docs/compatibility.md`.
- Published latency and resource figures come from `scripts/performance/` and
  the sanitized results in `performance/`. Do not estimate numbers in
  documentation.

## Protocol changes

A public protocol change updates the Go types in `pkg/protocol/`,
`api/openapi.yaml`, `api/protocol.schema.json`, the fixture in `api/fixtures/`,
and the consumer compatibility documentation in the same change.

## Pull request titles

Pull requests are squash-merged and the title becomes the commit subject on
`main`. Follow the format in `CONTRIBUTING.md`: `type(scope): imperative
summary`, lowercase, without a trailing period.

## Private notes

`.project-notes/` is ignored on purpose. Planning notes, research dumps, and
internal decision logs do not belong in commits or pull requests.
