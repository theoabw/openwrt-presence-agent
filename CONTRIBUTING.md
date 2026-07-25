# Contributing

Contributions are welcome, especially test results from OpenWrt routers and
firmware beyond the reference GL.iNet Flint 3. Documentation corrections,
sanitized platform fixtures, focused bug fixes, and provider improvements all
help.

Before opening an issue or pull request:

- search existing reports;
- state the router model, firmware name and exact version, and package
  architecture;
- explain which compatibility checklist steps passed or failed;
- include the smallest relevant, sanitized error or log excerpt; and
- remove bearer tokens, wireless credentials, MAC addresses, hostnames, router
  dumps, and other identifying client data.

See `docs/compatibility.md` for the capabilities and test evidence needed to
claim compatibility. A report that a package installed successfully is useful,
but it is not by itself sufficient for a support claim.

## Code changes

Changes must preserve the distinction between observations, connections, and
derived presence. Keep public operations read-only and provider-specific
payloads behind the normalized observation layer.

Public protocol changes must update OpenAPI and JSON Schema contracts in the same
change. User-visible behavior must include usage,
configuration, compatibility, and migration documentation.

Run the complete local check before submitting:

```sh
scripts/check.sh
```

It checks formatting, tests with the race detector, vet, shell scripts,
protocol and release consistency, API documents, and a reproducible package
build. It reports any missing host tools before starting. OpenWrt package
changes intended for an additional target must also be built with that target's
matching SDK.

The main code paths are:

- `internal/providers/` discovers platform sources and publishes normalized
  observations;
- `internal/engine/` owns bounded client state, reconciliation, ordering, and
  subscribers;
- `internal/api/` exposes the authenticated read-only HTTP and WebSocket API;
- `pkg/protocol/` contains stable public wire types; and
- `api/fixtures/` contains sanitized cross-consumer contract examples.

Protocol changes must update the Go types, OpenAPI, JSON Schema, fixture, and
consumer compatibility documentation together.

Private planning notes, product specifications, research dumps, and internal
decision logs do not belong in commits. Keep them in the ignored
`.project-notes/` directory when useful locally.

AI tools may assist a contribution, but the contributor remains responsible for
the submitted code, documentation, tests, provenance, and licensing. Please
review and understand every submitted change.
