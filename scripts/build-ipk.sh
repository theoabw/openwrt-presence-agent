#!/bin/sh
set -eu

usage() {
	echo "usage: build-ipk.sh VERSION PACKAGE_ARCH GOARCH OUTPUT_DIR" >&2
	exit 2
}

[ "$#" -eq 4 ] || usage

version="$1"
package_arch="$2"
goarch="$3"
output_dir="$(mkdir -p "$4" && realpath "$4")"

case "$version" in
	''|*[!0-9A-Za-z.+~-]*)
		echo "invalid package version: $version" >&2
		exit 2
		;;
esac
case "$package_arch" in
	''|*[!0-9A-Za-z_.-]*)
		echo "invalid package architecture: $package_arch" >&2
		exit 2
		;;
esac
case "$goarch" in
	amd64|arm64|mips|mipsle) ;;
	*)
		echo "unsupported Go architecture: $goarch" >&2
		exit 2
		;;
esac

repo_dir="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

source_date_epoch="${SOURCE_DATE_EPOCH:-$(git -C "$repo_dir" log -1 --format=%ct)}"
package_name="openwrt-presence-agent_${version}-1_${package_arch}.ipk"

mkdir -p \
	"$work_dir/control" \
	"$work_dir/data/etc/config" \
	"$work_dir/data/etc/init.d" \
	"$work_dir/data/etc/openwrt-presence-agent" \
	"$work_dir/data/usr/bin" \
	"$work_dir/data/usr/share/licenses/openwrt-presence-agent"

CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" \
	go build -C "$repo_dir" -trimpath \
	-ldflags="-s -w -buildid= -X main.version=$version" \
	-o "$work_dir/data/usr/bin/openwrt-presence-agent" \
	./cmd/openwrt-presence-agent

cp "$repo_dir/packaging/openwrt/files/openwrt-presence-agent.config" \
	"$work_dir/data/etc/config/openwrt-presence-agent"
cp "$repo_dir/packaging/openwrt/files/openwrt-presence-agent.init" \
	"$work_dir/data/etc/init.d/openwrt-presence-agent"
cp "$repo_dir/LICENSE" "$repo_dir/NOTICE" \
	"$work_dir/data/usr/share/licenses/openwrt-presence-agent/"
chmod 0755 \
	"$work_dir/data/usr/bin/openwrt-presence-agent" \
	"$work_dir/data/etc/init.d/openwrt-presence-agent"
chmod 0644 \
	"$work_dir/data/etc/config/openwrt-presence-agent" \
	"$work_dir/data/usr/share/licenses/openwrt-presence-agent/LICENSE" \
	"$work_dir/data/usr/share/licenses/openwrt-presence-agent/NOTICE"

cat > "$work_dir/control/control" <<EOF
Package: openwrt-presence-agent
Version: ${version}-1
Architecture: ${package_arch}
Maintainer: Theo Wilenius
Section: net
Priority: optional
License: Apache-2.0
Description: Event-driven OpenWrt client observation service
 Publishes authenticated Wi-Fi client snapshots and low-latency events.
EOF

cat > "$work_dir/control/conffiles" <<'EOF'
/etc/config/openwrt-presence-agent
/etc/openwrt-presence-agent/
EOF

cat > "$work_dir/control/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT:-}" ] && exit 0
umask 077
mkdir -p /etc/openwrt-presence-agent
if [ ! -s /etc/openwrt-presence-agent/token ]; then
	head -c 32 /dev/urandom | base64 > /etc/openwrt-presence-agent/token
fi
chmod 600 /etc/openwrt-presence-agent/token
exit 0
EOF
chmod 0755 "$work_dir/control/postinst"

tar_reproducible() {
	directory="$1"
	archive="$2"
	tar --sort=name \
		--mtime="@${source_date_epoch}" \
		--owner=0 --group=0 --numeric-owner \
		-C "$directory" -cf - . |
		gzip -n -9 > "$archive"
}

tar_reproducible "$work_dir/control" "$work_dir/control.tar.gz"
tar_reproducible "$work_dir/data" "$work_dir/data.tar.gz"
printf '2.0\n' > "$work_dir/debian-binary"
touch -d "@${source_date_epoch}" \
	"$work_dir/debian-binary" \
	"$work_dir/control.tar.gz" \
	"$work_dir/data.tar.gz"

(
	cd "$work_dir"
	ar rcD "$output_dir/$package_name" \
		debian-binary control.tar.gz data.tar.gz
)

echo "$output_dir/$package_name"
