#!/bin/sh
set -eu

repository="$(CDPATH='' cd -- "$(dirname -- "$0")/../.." && pwd)"
: "${ROUTER_SSH_TARGET:?set ROUTER_SSH_TARGET to the router SSH destination}"
router="$ROUTER_SSH_TARGET"
count="${BENCHMARK_COUNT:-10}"
output="${1:-$repository/performance-results}"
case "$count" in
	*[!0-9]* | 0) echo "BENCHMARK_COUNT must be a positive integer" >&2; exit 2 ;;
esac
mkdir -p "$output"

commit="$(git -C "$repository" rev-parse HEAD)"
{
	printf 'commit=%s\n' "$commit"
	printf 'host_go=%s\n' "$(go version)"
	printf 'host_uname=%s\n' "$(uname -a)"
} > "$output/environment.txt"

(
	cd "$repository"
	go test ./internal/engine \
		-run '^$' \
		-bench 'BenchmarkReconcile' \
		-benchmem \
		-count "$count"
) > "$output/engine-host.txt"

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
	go test -C "$repository" -c ./internal/engine \
	-o "$work_dir/openwrt-observer-engine.test"

# -O remains compatible with minimal Dropbear installations and routers that
# also provide the optional SFTP subsystem.
scp -O \
	"$work_dir/openwrt-observer-engine.test" \
	"$router:/tmp/openwrt-observer-engine.test"
ssh "$router" "chmod 700 /tmp/openwrt-observer-engine.test"
ssh "$router" "uname -a; ubus call system board" \
	> "$output/router-environment-raw.txt"
# shellcheck disable=SC2029 # count is validated locally as a positive integer.
ssh "$router" \
	"/tmp/openwrt-observer-engine.test -test.run '^$' -test.bench 'BenchmarkReconcile' -test.benchmem -test.count '$count'" \
	> "$output/engine-router.txt"

printf 'Results written to %s\n' "$output"
