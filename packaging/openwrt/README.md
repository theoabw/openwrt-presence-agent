# OpenWrt packaging

This directory is a native OpenWrt package definition. It installs the daemon,
UCI defaults, a `procd` init script, and the project license and notice. The
package creates a 256-bit bearer token on first installation, preserves it
across upgrades, binds to loopback by default, and does not create firewall
rules.

For a local package build, prepare an immutable one-package feed from the
current Git commit:

```sh
scripts/prepare-openwrt-feed.sh .openwrt-feed
```

The generated recipe contains the archive's SHA-256 hash rather than
`PKG_HASH:=skip`. Add `.openwrt-feed` as a local SDK feed, install it, and run:

```sh
make package/openwrt-presence-agent/compile V=s
```

The source archive contains a top-level versioned directory. The official SDK
CI follows this same process and preserves the resulting packages as workflow
artifacts.

Package contents can be checked without a router using
`scripts/check-package.sh path/to/package.ipk`.

`scripts/build-ipk.sh` is a local fallback for the vendor-specific target for
which no matching public SDK exists. It is not the source of vanilla OpenWrt
compatibility claims.
