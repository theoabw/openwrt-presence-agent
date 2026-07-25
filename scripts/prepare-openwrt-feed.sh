#!/bin/sh
set -eu

usage() {
	echo "usage: prepare-openwrt-feed.sh OUTPUT_DIR [GIT_REF]" >&2
	exit 2
}

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
	usage
fi

repo_dir="$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)"
output_dir="$1"
git_ref="${2:-HEAD}"
version="$(sed -n 's/^PKG_VERSION:=//p' "$repo_dir/packaging/openwrt/Makefile")"
package_dir="$output_dir/openwrt-presence-agent"
archive_name="openwrt-presence-agent-${version}.tar.gz"

case "$output_dir" in
'' | /)
	echo "refusing unsafe output directory: $output_dir" >&2
	exit 2
	;;
esac

mkdir -p "$package_dir"
cp "$repo_dir/packaging/openwrt/Makefile" "$package_dir/Makefile"
cp -R "$repo_dir/packaging/openwrt/files" "$package_dir/files"

git -C "$repo_dir" archive \
	--format=tar \
	--prefix="openwrt-presence-agent-${version}/" \
	"$git_ref" |
	gzip -n -9 > "$package_dir/$archive_name"

archive_hash="$(sha256sum "$package_dir/$archive_name" | awk '{print $1}')"
# The generated Makefile must contain the literal OpenWrt make variable.
# shellcheck disable=SC2016
sed -i \
	-e 's|^PKG_SOURCE_URL:=.*|PKG_SOURCE_URL:=file://$(CURDIR)|' \
	-e "s|^PKG_HASH:=.*|PKG_HASH:=$archive_hash|" \
	"$package_dir/Makefile"

git -C "$output_dir" init -q
git -C "$output_dir" config user.name "OpenWrt Presence build"
git -C "$output_dir" config user.email "build@localhost"
git -C "$output_dir" config commit.gpgsign false
git -C "$output_dir" add openwrt-presence-agent
git -C "$output_dir" commit -q -m "build: prepare OpenWrt package feed"

printf '%s\n' "$package_dir"
