# Configuration and credentials

UCI section `openwrt-presence-agent.main` is authoritative on OpenWrt. The init
script converts each option to an explicit daemon argument.

| Option | Default | Bounds and meaning |
|---|---:|---|
| `enabled` | `1` | Start this instance. |
| `listen_address` | `127.0.0.1` | Literal IPv4 or IPv6 address. |
| `port` | `8787` | `1`–`65535`. |
| `token_file` | `/etc/openwrt-presence-agent/token` | Absolute credential path. |
| `agent_id_file` | `/etc/openwrt-presence-agent/agent-id` | Absolute stable-ID path. |
| `ubus_path` | `/bin/ubus` | Absolute path to the local ubus client. |
| `hostapd_socket` | `/var/run/hostapd/global` | Absolute path to the hostapd global control socket. |
| `arping_path` | `/usr/bin/arping` | Absolute path to the packaged active ARP probe. |
| `dhcp_leases_file` | `/tmp/dhcp.leases` | Absolute dnsmasq lease file used to find wired probe candidates. |
| `lan_interface` | `br-lan` | Interface on which wired clients are probed. |
| `provider` | `ubus` | Only implemented provider. |
| `reconcile_interval` | `30s` | At least one second. |
| `wired_reconcile_interval` | `2s` | Active Ethernet probe cycle; at least one second. |
| `discovery_interval` | `10s` | At least one second. |
| `command_timeout` | `5s` | At least one second. |
| `max_command_output` | `1048576` | 4 KiB–16 MiB. |
| `max_event_bytes` | `65536` | 1 KiB–1 MiB. |
| `max_clients` | `512` | `1`–`100000`. |
| `max_http_connections` | `16` | `1`–`4096`. |
| `provider_queue_size` | `256` | `1`–`65536` observations. |
| `max_stream_clients` | `4` | `1`–`1024`. |
| `stream_queue_size` | `64` | `1`–`4096` events per stream. |
| `log_level` | `info` | `error`, `warn`, `info`, or `debug`. |
| `departure_delay` | `0s` | Hold a client present after its last connection is lost before announcing departure. `0` (default) is immediate and literal. When enabled it must be `1s`–`10m`.

Invalid values stop startup with a clear error. The listener deliberately does
not derive or guess a LAN address: ambiguous automatic binding falls back to
loopback through the packaged default.

Ethernet presence is intentionally stricter than lease or stale neighbor-table
presence. A fresh Linux `NUD_REACHABLE` IP-neighbor notification can mark a
wired client present immediately. Every `wired_reconcile_interval`, the agent
also sends one ARP request to each eligible leased address on `lan_interface`;
a fresh reply confirms presence and the completed sweep confirms absence. An
unexpired DHCP lease, stale/failed neighbor entry, or bridge forwarding entry
never counts as online.

The two-second default provides approximately one-second average sampling
delay. Offline detection can additionally require the roughly one-second ARP
timeout, so its practical worst case is about three seconds plus scheduling
overhead. Wake detection can be faster when the client generates a fresh
neighbor event, but operating-system and NIC resume time remains outside the
agent's control. Static-address clients need a dnsmasq lease/reservation for
active probing, although fresh kernel neighbor events can discover them while
they are communicating.

Clients currently associated with any hostapd BSS are excluded before wired
probes are launched. Their presence remains entirely event-driven, so the wired
fallback cannot add latency to Wi-Fi disconnects or local roaming.
On startup, wired probing waits for either the first authoritative hostapd
snapshot or a definitive Wi-Fi-unavailable status. This prevents an initial
wired sweep from racing the Wi-Fi exclusion inventory without blocking
Ethernet indefinitely on a router with unusable radios.

## Departure delay

By default a client that loses its last known connection is reported
immediately (`unknown` until an authoritative snapshot confirms `absent`). Some
phones and roaming clients briefly drop and rejoin the same radio, producing a
short presence flap for consumers. Setting `departure_delay` holds such a
client `present` for the configured window after its last connection is lost:

- A disassociation, or an authoritative snapshot that would remove the last
  live connection, starts a hold instead of an immediate departure.
- If the client reconnects before the hold expires, no departure is ever
  announced: the flap is absorbed.
- If the hold expires, the departure is announced once (reason
  `departure_delay`) with the same eventual state as the immediate path.
- A provider becoming unavailable is stream-integrity loss, not a client
  departure, and is never delayed.

The window also applies to clients absent from an authoritative snapshot, so
it caps the worst-case departure announcement delay rather than leaving a
gap. Keep `departure_delay` short relative to `reconcile_interval` for
predictable behavior. Diagnostics report the current number of holds as
`pending_departures`.

## Token handling

Read the token only into a trusted client:

```sh
ssh root@ROUTER 'cat /etc/openwrt-presence-agent/token'
```

Bearer authentication without TLS can be observed by another system able to
capture traffic on the same segment. Keep the API on a trusted management LAN,
use an existing authenticated reverse proxy for TLS, or access the loopback
listener through an SSH tunnel. CORS is not enabled.

To rotate the token without overlap, write a new Base64 encoding of 32 random
bytes to a temporary file, set mode `0600`, atomically replace the configured
token file, and reload the service. Existing WebSocket connections end during
reload. The daemon rejects token files accessible by users outside the owner and
group; the packaged file is owner-only.

Diagnostics report bounds, listeners, provider health, counts, and memory. They
never include the token, authorization headers, environment, router
configuration, or wireless credentials.
