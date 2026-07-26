# API and state semantics

The v1 API is read-only and independent of OpenWrt-native payloads. All routes
require bearer authentication. Responses are JSON and use UTC RFC 3339
timestamps.

`GET /v1/clients` returns the current internally consistent engine snapshot
with a random runtime `stream_epoch` and monotonically increasing `sequence`.
Before the first successful provider snapshot, this response can be empty and
is not evidence that no clients are connected. Check `GET /v1/health` and
provider status before treating client state as provider-authoritative. Each
client has provider-independent connections and a derived state:

- `present` / `present: true`: at least one current Wi-Fi connection or
  actively ARP-responsive Ethernet connection exists;
- `absent` / `present: false`: an authoritative snapshot or scoped event
  established that no discovered connection remains;
- `unknown` / `present: null`: provider failure made the retained evidence
  uncertain.

A disassociation is source-scoped. During a local roam, removing the old
connection does not make the client absent if another BSS connection is already
known. If it removes the last known connection, state becomes `unknown` while
the provider immediately takes an all-BSS snapshot. That snapshot confirms
absence or discovers the new connection. This is correctness recovery rather
than a time-based presence debounce.

`GET /v1/health` returns:

- HTTP 503 before any authoritative provider snapshot;
- HTTP 200 with `operational` while the provider is healthy;
- HTTP 200 with `degraded` if a provider fails after a valid snapshot.

## WebSocket stream

Upgrade `GET /v1/events` using RFC 6455. The server sends:

1. `stream.hello`, including protocol version and replay capability;
2. `state.snapshot`, containing current internally consistent state captured
   atomically with subscription registration;
3. state events in strictly increasing sequence order;
4. `stream.heartbeat` every 30 seconds without advancing sequence.

Clients may remain receive-only and are not required to send application
messages or WebSocket ping frames. The server detects failed and slow
connections through bounded heartbeat writes.

State event types currently include `client.updated`,
`client.presence_changed`, `client.removed`, `provider.status`, and
`state.resynchronized`. Client events contain the resulting connection set, so
one observation produces one client envelope instead of separate connection
and client events. The server also sends `stream.shutdown` during a graceful
service stop when the connection remains writable. Presence and corrective
events include a reason.

There is no event replay. A slow consumer whose bounded queue fills is
disconnected. Reconnect and accept the next snapshot after any closure, epoch
change, or sequence gap. Filtering and mutation operations are not exposed.

Endpoint and response definitions are in `api/openapi.yaml`; reusable snapshot,
client, connection, and event payloads are in `api/protocol.schema.json`.
