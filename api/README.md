# API contracts

These contracts are consumer-neutral. Home Assistant is one implementation of
the protocol; other trusted local applications can implement the same public
HTTP and WebSocket behavior without depending on Home Assistant code.

The authenticated, read-only API is described by [OpenAPI](openapi.yaml).
WebSocket messages and reusable response objects are defined by
[protocol.schema.json](protocol.schema.json).

All endpoints use the `/v1` major-version prefix. Additive fields and event
types may be introduced without changing that prefix. Consumers must reconnect
and accept the initial internally consistent snapshot whenever the stream epoch
changes, the connection closes, or a sequence gap is detected. Consumers must
also check health and retry state endpoints that return HTTP 503 while the
initial authoritative Wi-Fi snapshot is unavailable.

`GET /v1/info` advertises separate `wifi_snapshot`, `wifi_events`,
`wired_snapshot`, and `wired_events` capabilities. Wired events include fresh
Linux route-neighbor reachability and active ARP replies; wired snapshots are
the authoritative active-probe reconciliation.

Timestamps are UTC RFC 3339 values. Client IDs use the form
`mac:aa:bb:cc:dd:ee:ff`. The nullable `present` field is `null` when provider
failure makes state uncertain.

[`fixtures/v1.json`](fixtures/v1.json) is the sanitized interoperability
fixture. Agent tests decode it through the public Go types, and maintained
consumers should parse a mirrored copy in their own test suite. A protocol
change is incomplete until the contracts and fixture agree.
