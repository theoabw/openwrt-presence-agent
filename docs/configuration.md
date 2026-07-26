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
| `arping_path` | `/usr/sbin/arping` | Absolute path to the packaged active ARP probe. |
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

Invalid values stop startup with a clear error. The listener deliberately does
not derive or guess a LAN address: ambiguous automatic binding falls back to
loopback through the packaged default.

Ethernet presence is intentionally stricter than lease or neighbor-table
presence. Every `wired_reconcile_interval`, the agent sends one ARP request to each
leased address on `lan_interface`. Only a fresh reply counts as online. An
unexpired DHCP lease, or a stale kernel neighbor entry, never does. Consequently
a sleeping, unplugged, or powered-off wired client becomes absent after the
next reconciliation. The two-second default provides approximately one-second
average and two-second worst-case detection, plus probe and scheduling time.
Static-address clients need a dnsmasq lease/reservation so the agent has an
address to probe.

Clients currently associated with any hostapd BSS are excluded before wired
probes are launched. Their presence remains entirely event-driven, so the wired
fallback cannot add latency to Wi-Fi disconnects or local roaming.

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
