# Compatibility and troubleshooting

The initial provider requires:

- UCI and `procd` for native configuration and supervision;
- an executable ubus client, `/bin/ubus` by default;
- dynamic objects named `hostapd.*`;
- a `get_clients` method on each discovered object; and
- a hostapd global control socket, `/var/run/hostapd/global` by default, that accepts
  `ATTACH` and emits `IFNAME`-scoped `AP-STA-CONNECTED` and
  `AP-STA-DISCONNECTED` events.

The daemon enumerates objects and never requires configured radio or interface
names. Event `IFNAME` values are accepted only when they match a currently
discovered `hostapd.*` object. It deliberately does not call broad
wireless-status methods because some vendor firmware includes plaintext
wireless keys in those responses.

Both paths are configurable with `ubus_path` and `hostapd_socket`; this avoids
requiring vendor firmware to use the vanilla filesystem layout.

The provider intentionally uses two local hostapd interfaces: ubus provides
structured discovery and authoritative snapshots, while the standard hostapd
global control socket provides low-latency station events. Some firmware,
including the inspected Flint 3 build, exposes subscribable ubus objects but
does not publish association notifications through them. This split is
capability specific, not a hard-coded list of Flint 3 interfaces, but alternate
socket layouts and vendor event formats are not currently supported.

## Support levels

Compatibility claims use distinct evidence levels:

| Level | Evidence |
|---|---|
| SDK build-tested | The official OpenWrt SDK produced a native package for the named release and architecture. |
| Vanilla VM-tested | The package installed and passed the runtime smoke test on a named vanilla OpenWrt virtual machine. |
| Simulated-Wi-Fi-tested | Association and disassociation passed with OpenWrt hostapd and `mac80211_hwsim`. |
| Hardware-tested | The complete acceptance checklist passed on the named router, firmware, and wireless driver. |

An SDK build proves package and CPU compatibility, not wireless-driver
behavior. An x86 virtual machine exercises the same UCI, procd, ubus, package,
and daemon lifecycle used by other targets, but cannot by itself establish
hardware support.

The CI matrix builds with official OpenWrt 25.12 and 24.10 SDKs for `x86_64`,
`aarch64_cortex-a53`, `arm_cortex-a7_neon-vfpv4`, `mips_24kc`, and
`mipsel_24kc`; 25.12 additionally covers `aarch64_generic`. Results remain
build-tested until the corresponding workflow has passed. Do not describe a
router as hardware-tested based solely on this matrix.

## Only hardware-tested platform

| Device | Vendor firmware | OpenWrt base | Kernel | Target | Package architecture |
|---|---|---|---|---|---|
| GL.iNet Flint 3 (`GL-BE9300`) | 4.9.0 | `23.05-SNAPSHOT` | `5.4.213` | `ipq53xx/generic` | `aarch64_cortex-a53_neon-vfpv4` |

Package installation, `procd` startup, dynamic discovery across all active BSS
instances, authoritative snapshots, low-latency association and disassociation
events, API consumption, observer restart, and wireless reload have been
verified on this platform.

This is currently the complete hardware-tested list. In particular, the Flint
2 has not been live-tested by the project. Availability of a matching SDK-built
package indicates build compatibility only.

A release may claim hardware support only after package installation, startup,
snapshots, association, disassociation, local roaming, consumer reconnect,
observer restart, and wireless reload have been verified on an exactly named
firmware build. Router and firmware combinations other than the table above are
unverified even if their capabilities appear compatible.

## Testing another router

Before sharing a report, remove tokens, wireless credentials, MAC addresses,
hostnames, and other identifying data. Include:

- router model and hardware revision;
- firmware distribution and exact version;
- sanitized output from `ubus call system board`;
- the relevant architecture output from `apk --print-arch` on OpenWrt 25.12 or
  `opkg print-architecture` on older releases;
- whether installation and `procd` startup succeeded;
- whether initial snapshots, association, disassociation, and local roaming
  were observed;
- whether reconnecting a consumer, restarting the observer, and reloading
  wireless recovered correctly; and
- any sanitized errors, alternate socket paths, or provider behavior.

Please use the short
[router compatibility report](https://github.com/theoabw/openwrt-presence-agent/issues/new?template=router-compatibility.yml)
to share the results. Passing the complete checklist can justify adding a named
firmware build to this page, but it is not a prerequisite for reaching out.
Partial tests, failures, alternate layouts, and simple “it worked” reports all
help identify portability work and build a more representative compatibility
picture.

## Troubleshooting

HTTP 503 from `/v1/health` means no authoritative snapshot is available. Check
`/v1/providers` and logs for missing objects, missing `get_clients`, ubus
permissions, hostapd global-socket access, timeouts, malformed responses, or
configured output limits.

An overall `status: degraded` means the API retains a prior snapshot but
provider evidence is currently uncertain. The provider keeps rediscovering,
reattaching to the event socket, and reconciling without terminating the API.

Frequent WebSocket reconnects with increasing
`dropped_stream_clients` indicate a slow consumer. Process events faster or
increase `stream_queue_size` within available router memory.

An unsafe-token-permissions startup error requires removing access for other
users. The package default is `0600`.

If `get_clients` exceeds the configured command-output limit, increase
`max_command_output` and reload. Do not remove the bound entirely.
