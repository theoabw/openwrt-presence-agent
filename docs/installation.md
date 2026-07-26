# Installation and lifecycle

The intended installation unit is a native OpenWrt package: `.apk` on OpenWrt
25.12 and newer, or `.ipk` on OpenWrt 24.10 and older. Direct installation
from a release is the primary installation path because vendor package
registries may lag or omit the package.

## Choose and verify a package

Check the OpenWrt version and package architecture:

```sh
cat /etc/openwrt_release
apk --print-arch                 # OpenWrt 25.12+
opkg print-architecture          # OpenWrt 24.10 and older
```

Download the matching package and its published SHA-256 value from the
[GitHub Releases page](https://github.com/theoabw/openwrt-presence-agent/releases)
to a trusted computer. The package target must match the router firmware; a
similar router model name is not sufficient. If no release asset matches, do
not force installation. Follow the [package build guide](development.md) or
open a compatibility request instead.

From the directory containing the downloads, calculate the package checksum:

```sh
sha256sum openwrt-presence-agent_VERSION_RELEASE_ARCH.apk
```

Compare the complete result with the SHA-256 value published for that exact
release asset. Do not continue if they differ.

## Install directly

Copy the verified package to temporary storage on the router, install that
exact local file, and start the service:

```sh
scp openwrt-presence-agent_VERSION-RELEASE_ARCH.apk root@ROUTER:/tmp/
ssh root@ROUTER
apk add --allow-untrusted /tmp/openwrt-presence-agent_VERSION-RELEASE_ARCH.apk
/etc/init.d/openwrt-presence-agent enable
/etc/init.d/openwrt-presence-agent start
```

On OpenWrt 24.10 or older, transfer the matching `.ipk` and use
`opkg install /tmp/PACKAGE.ipk` instead. `--allow-untrusted` permits a directly
downloaded package whose published SHA-256 value you have verified; it does not
disable signature checks for configured feeds.

Using `/tmp` avoids an unnecessary persistent write for the transferred
package. After a successful installation, it may be removed from `/tmp`; the
installed service is unaffected.

The package generates `/etc/openwrt-presence-agent/token` with 256 random bits
and mode `0600`. The daemon creates a stable random `agent-id` alongside it.
Both files and `/etc/config/openwrt-presence-agent` are preserved as
configuration during upgrades.

## Verify first API access

The listener defaults to router loopback. On the router, read the token into a
temporary shell variable, request service health, and then unset the variable:

```sh
OBSERVER_TOKEN="$(cat /etc/openwrt-presence-agent/token)"
wget -qO- \
  --header="Authorization: Bearer ${OBSERVER_TOKEN}" \
  http://127.0.0.1:8787/v1/health
unset OBSERVER_TOKEN
```

Do not paste the token into issue reports or logs. If the request fails,
inspect:

```sh
/etc/init.d/openwrt-presence-agent status
logread -e openwrt-presence-agent
```

## Network access

The default listener is `127.0.0.1:8787`. The package never creates a firewall
rule. To expose it on a trusted LAN, explicitly set a literal LAN address after
checking the interface and firewall zone:

```sh
uci set openwrt-presence-agent.main.listen_address='192.0.2.1'
uci commit openwrt-presence-agent
/etc/init.d/openwrt-presence-agent reload
```

`192.0.2.1` is documentation-only; substitute an address assigned to the
router's trusted LAN. Do not use a WAN address. Wildcard addresses are accepted
only when explicitly configured and are not recommended; the package does not
open the firewall.

Bearer authentication without TLS is appropriate only on a trusted management
network. See [configuration and credentials](configuration.md) for SSH tunnel
and TLS guidance.

## Upgrade, rollback, and removal

Upgrade by verifying and copying the new release package as above, then
installing its explicit local path:

```sh
apk add --allow-untrusted /tmp/openwrt-presence-agent_NEW_VERSION_ARCH.apk
```

Use `opkg install /tmp/PACKAGE.ipk` on 24.10 and older. Feed installation is
secondary and should be used only when a trusted, current feed explicitly
provides the intended version.

Stopping and restarting creates a new stream epoch and a new internally
consistent initial stream snapshot. Provider reconciliation establishes
authoritative Wi-Fi and wired state after startup. Wired probing begins after
the initial Wi-Fi inventory is available, so associated Wi-Fi clients are not
duplicated as Ethernet connections. Provider failures are retried inside the
daemon; `procd` applies a bounded respawn policy if the whole process fails. UCI
changes restart the service through a reload trigger.

Normal package removal follows OpenWrt conffile policy and preserves modified
configuration:

```sh
apk del openwrt-presence-agent
```

Use `opkg remove openwrt-presence-agent` on 24.10 and older. Before rollback,
download and verify the exact previous package, copy it to `/tmp`, and install
its explicit filename with the applicable package manager. Then check the
service status and `/v1/health` as above. Configuration options in the current
MVP are backward-compatible within the `0.1.x` line.

Removal may preserve `/etc/config/openwrt-presence-agent` and the credentials
under `/etc/openwrt-presence-agent`. To purge them, first remove the package,
confirm that `/etc/openwrt-presence-agent` is the intended directory, and then
delete those files manually. This permanently removes the bearer token and
stable agent identity.
