#!/bin/sh
set -eu

package_path="${1:?usage: check-package.sh PACKAGE.ipk}"
package_path="$(realpath "$package_path")"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

(cd "$work_dir" && ar x "$package_path")
control_archive="$(find "$work_dir" -maxdepth 1 -name 'control.tar.*' -print -quit)"
data_archive="$(find "$work_dir" -maxdepth 1 -name 'data.tar.*' -print -quit)"
[ -n "$control_archive" ]
[ -n "$data_archive" ]
tar -xf "$control_archive" -C "$work_dir"
tar -tf "$data_archive" > "$work_dir/contents"

grep -qx 'Package: openwrt-presence-agent' "$work_dir/control"
grep -Eq '^Version: [0-9A-Za-z.+~-]+-1$' "$work_dir/control"
grep -Eq '^Architecture: [0-9A-Za-z_.-]+$' "$work_dir/control"

for required in \
	./usr/bin/openwrt-presence-agent \
	./etc/config/openwrt-presence-agent \
	./etc/init.d/openwrt-presence-agent \
	./usr/share/licenses/openwrt-presence-agent/LICENSE \
	./usr/share/licenses/openwrt-presence-agent/NOTICE
do
	grep -qx "$required" "$work_dir/contents"
done

if grep -Eq 'firewall|\\.project-notes|/token$|/agent-id$' "$work_dir/contents"; then
	echo "package contains forbidden generated or firewall content" >&2
	exit 1
fi

binary_entry="$(tar -tf "$data_archive" | grep -E '^\./usr/bin/openwrt-presence-agent$')"
[ -n "$binary_entry" ]
tar -xf "$data_archive" -C "$work_dir" "$binary_entry"
file "$work_dir/usr/bin/openwrt-presence-agent" |
	grep -q 'ELF 64-bit LSB.*statically linked'

echo "package contents are valid"
