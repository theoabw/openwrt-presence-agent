# Building a consumer

OpenWrt Presence Agent is a platform-neutral, read-only observation service.
Home Assistant is the first maintained consumer, but it does not receive a
private or privileged interface. Any trusted local application can implement
the same versioned API contract.

Potential consumers include:

- other home-automation platforms;
- local dashboards and notification services;
- network inventory or client-activity monitors; and
- small adapters that translate observations into another local protocol.

These are integration possibilities, not bundled features. The agent does not
send webhooks, write databases, publish MQTT, or make outbound connections.

## Recommended integration pattern

1. Call `GET /v1/info` and verify the protocol version and stable agent ID.
2. Check `GET /v1/health` before treating client state as authoritative.
3. Connect to `/v1/events` with the bearer token.
4. Accept `stream.hello` followed by the complete `state.snapshot`.
5. Apply subsequent events only while their epoch and sequence remain
   continuous.
6. On a disconnect, sequence gap, epoch change, or malformed state-changing
   event, mark the source uncertain and reconnect for a new snapshot.

Do not turn transport failure into client absence. Do not infer global absence
from one radio disassociation: the normalized client state already accounts for
connections across discovered BSS instances.

The authoritative wire definitions are:

- [`openapi.yaml`](../api/openapi.yaml) for HTTP operations;
- [`protocol.schema.json`](../api/protocol.schema.json) for payloads; and
- the [API and state semantics guide](api.md) for lifecycle rules.

## Security and privacy

Treat the bearer token like a password and send it only to the agent. The
current server is plain HTTP by default, so use a trusted isolated network or a
TLS reverse proxy when another system can observe the path.

Client identifiers and association history can reveal household routines.
Consumers should minimize retention, avoid unnecessary attributes, protect
their own logs and storage, and never expose the agent API directly to the
internet.

The public API is intentionally read-only. A consumer must not need router
credentials, shell access, generic ubus access, or wireless configuration.
