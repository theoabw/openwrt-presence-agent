# Development and package builds

The project requires Go 1.25 or later. The canonical local validation command
is:

```sh
scripts/check.sh
```

The script names any missing host tools and runs the same repository-owned
checks as CI: formatting, race-enabled tests, vet, shell validation, metadata
consistency, API document parsing, and the verified package build.

Implementation packages remain under `internal`; only the stable public
payloads live in `pkg/protocol`. Provider code must not import API transports,
and public transports must not expose ubus-native payloads.

Platform integrations implement the `internal/providers.Provider` lifecycle
interface and publish only normalized observations, authoritative snapshots,
and provider status through `observation.Sink`. Add a provider-specific package
under `internal/providers`, give it its own configuration type, and register its
constructor in `internal/providers.New`. The state engine and API must remain
independent of platform-native discovery, event, and snapshot payloads.

For a release-style binary:

```sh
CGO_ENABLED=0 go build -trimpath \
  -ldflags='-s -w -buildid=' \
  ./cmd/openwrt-presence-agent
```

To build a native package, use an official OpenWrt SDK with the matching target.
Create the hashed local feed, add it to the SDK, and inspect the result:

```sh
scripts/prepare-openwrt-feed.sh .openwrt-feed 0.0.0
scripts/check-package.sh bin/packages/ARCH/base/openwrt-presence-agent_*.ipk
```

The checked-in OpenWrt recipe is a build template and deliberately keeps
`PKG_VERSION:=0.0.0`. Pull-request SDK builds use that development version.
Release builds take the real version from the signed `vMAJOR.MINOR.PATCH` tag
and inject it into the generated feed recipe and package artifacts. Contributors
therefore do not choose or commit release versions.

The `OpenWrt SDK` workflow performs this build against OpenWrt 25.12 and 24.10
for the common architecture matrix documented in
[compatibility](compatibility.md). OpenWrt 25.12 emits `.apk`; 24.10 emits
`.ipk`.

## Vanilla OpenWrt runtime test

Boot an official x86-64 OpenWrt image with QEMU, configure SSH access, and build
the matching `x86_64` package through the SDK workflow. Then run from the
development computer:

```sh
scripts/openwrt-runtime-smoke.sh root@OPENWRT_VM package.apk
```

The test transfers the package without requiring SFTP, installs it, enables and
restarts the procd service, authenticates to the local provider endpoint, and
checks that the generated token and observer identity survive a restart. It
intentionally leaves the package installed for further association testing.
Run it only against a disposable test VM.

The official [OpenWrt QEMU guest
guide](https://openwrt.org/docs/guide-user/virtualization/qemu) documents the
base VM setup. A VM without a radio should report a degraded provider rather
than terminating the API.

For a real event-path test, use a VM image containing `wpad` and
`kmod-mac80211-hwsim`, configure one virtual radio as an AP, and associate a
virtual station. That test can establish simulated-Wi-Fi support but does not
replace named physical-router reports.

The tag-triggered release workflow builds vanilla packages through every
official SDK in the documented release matrix. It also uses
`scripts/build-ipk.sh` to create a reproducible, self-contained package for the
exact architecture verified on the hardware-tested vendor firmware, which has no
matching public SDK. To test that vendor build locally:

```sh
scripts/build-ipk.sh \
  0.1.0 aarch64_cortex-a53_neon-vfpv4 arm64 dist
scripts/check-package.sh dist/openwrt-presence-agent_*.ipk
```

## Public package release gate

Creating a SemVer tag such as `v0.1.0` runs tests, verifies that the tag agrees
with `PKG_VERSION`, builds the official SDK matrix and hardware-verified vendor
package, writes `SHA256SUMS`, and creates a GitHub release. Public repositories
also receive GitHub artifact provenance attestations. Before creating a public
binary release:

- record the exact router firmware, OpenWrt target, package architecture, and
  SDK used for every package;
- ensure the signed tag contains the intended release version and the release
  matrix contains only architectures supported by evidence;
- run `scripts/check-package.sh` on each resulting `.ipk`;
- complete the hardware acceptance checklist in `compatibility.md`;
- enable and test GitHub private vulnerability reporting;
- verify the generated SHA-256 value for every release asset; and
- make the Git tag, package version, release notes, and asset filenames agree.

Do not convert SDK build results into hardware claims. The generated SDK feed
pins its source archive hash; the checked-in `PKG_HASH:=skip` value exists only
as a template placeholder and is never used by the SDK workflow.

Sanitized provider variations belong under `testdata`. Never commit router
dumps, wireless configuration, bearer tokens, client identities, or material
from unlicensed prior art.
