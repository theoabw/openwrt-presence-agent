#!/bin/sh
set -eu

usage() {
	echo "usage: openwrt-runtime-smoke.sh SSH_TARGET PACKAGE" >&2
	exit 2
}

[ "$#" -eq 2 ] || usage

ssh_target="$1"
package_path="$(realpath "$2")"
package_name="$(basename "$package_path")"
remote_package="/tmp/$package_name"

case "$package_name" in
*[!0-9A-Za-z._+-]*)
	echo "package filename contains unsupported characters" >&2
	exit 2
	;;
esac

case "$package_name" in
*.apk) package_manager=apk ;;
*.ipk) package_manager=opkg ;;
*)
	echo "package must end in .apk or .ipk" >&2
	exit 2
	;;
esac

# package_name accepts only the package suffixes above and contains no shell
# metacharacters when produced by the SDK.
# shellcheck disable=SC2029
ssh "$ssh_target" "cat > '$remote_package'" < "$package_path"

ssh "$ssh_target" sh -s -- "$remote_package" "$package_manager" << 'REMOTE'
set -eu

package_path="$1"
package_manager="$2"

cleanup() {
	rm -f "$package_path"
}
trap cleanup EXIT

board="$(ubus call system board)"
printf '%s\n' "$board" | jsonfilter -e '@.release.distribution' -e '@.release.version'

case "$package_manager" in
	apk) apk add --allow-untrusted "$package_path" ;;
	opkg) opkg install "$package_path" ;;
esac

test -x /usr/bin/openwrt-presence-agent
test -x /etc/init.d/openwrt-presence-agent
test -s /etc/openwrt-presence-agent/token
test -s /etc/openwrt-presence-agent/agent-id || {
	/etc/init.d/openwrt-presence-agent enable
	/etc/init.d/openwrt-presence-agent restart
	sleep 2
}

token_before="$(sha256sum /etc/openwrt-presence-agent/token)"
agent_before="$(sha256sum /etc/openwrt-presence-agent/agent-id)"

/etc/init.d/openwrt-presence-agent enable
/etc/init.d/openwrt-presence-agent restart
sleep 2
/etc/init.d/openwrt-presence-agent running

token="$(cat /etc/openwrt-presence-agent/token)"
response="$(
	wget -qO- \
		--header="Authorization: Bearer $token" \
		http://127.0.0.1:8787/v1/providers
)"
printf '%s\n' "$response" | jsonfilter -e '@[0].id' | grep -qx 'ubus-hostapd'

/etc/init.d/openwrt-presence-agent restart
sleep 2
/etc/init.d/openwrt-presence-agent running
test "$token_before" = "$(sha256sum /etc/openwrt-presence-agent/token)"
test "$agent_before" = "$(sha256sum /etc/openwrt-presence-agent/agent-id)"

printf '%s\n' "vanilla OpenWrt runtime smoke test passed"
REMOTE
