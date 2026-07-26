#!/bin/sh
set -eu

repo_dir="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$repo_dir"

for command in go shellcheck jq ruby python3 file ar tar gzip; do
	command -v "$command" >/dev/null 2>&1 || {
		echo "missing required command: $command" >&2
		exit 1
	}
done

unformatted="$(find cmd internal pkg api -name '*.go' -type f -exec gofmt -l {} +)"
[ -z "$unformatted" ] || {
	echo "Go files need formatting:" >&2
	echo "$unformatted" >&2
	exit 1
}

go test -race ./...
go vet ./...
shellcheck -s sh \
	packaging/openwrt/files/openwrt-presence-agent.init \
	scripts/build-ipk.sh \
	scripts/check-package.sh \
	scripts/check.sh \
	scripts/openwrt-runtime-smoke.sh \
	scripts/prepare-openwrt-feed.sh \
	scripts/performance/run-engine-benchmarks.sh \
	scripts/performance/sample-router-resources.sh \
	testdata/fake-ubus.sh \
	testdata/fake-ubus-race.sh
python3 scripts/check-consistency.py
python3 - <<'PY'
from pathlib import Path

for path in Path("scripts").rglob("*.py"):
    compile(path.read_bytes(), path, "exec")
PY
jq empty api/protocol.schema.json api/fixtures/v1.json
ruby -e 'require "yaml"; YAML.load_file("api/openapi.yaml")'

package_dir="$(mktemp -d)"
feed_dir="$(mktemp -d)"
trap 'rm -rf "$package_dir" "$feed_dir"' EXIT
scripts/build-ipk.sh \
	"0.0.0" \
	aarch64_cortex-a53_neon-vfpv4 \
	arm64 \
	"$package_dir"
scripts/check-package.sh "$package_dir"/*.ipk

scripts/prepare-openwrt-feed.sh "$feed_dir" 9.8.7
test -f "$feed_dir/openwrt-presence-agent/openwrt-presence-agent-9.8.7.tar.gz"
grep -qx 'PKG_VERSION:=9.8.7' \
	"$feed_dir/openwrt-presence-agent/Makefile"
