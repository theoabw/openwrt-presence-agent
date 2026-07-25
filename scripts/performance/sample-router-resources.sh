#!/bin/sh
set -eu

duration="${1:-300}"
interval="${2:-1}"
pid="${OBSERVER_PID:-$(pidof openwrt-presence-agent)}"

[ -n "$pid" ] || {
	echo "openwrt-presence-agent is not running" >&2
	exit 1
}
case "$pid" in
	*[!0-9]*)
		echo "expected one observer PID, found: $pid" >&2
		exit 1
		;;
esac

printf 'unix_time,pid,cpu_ticks,rss_kib,vm_kib,threads\n'
started="$(date +%s)"
while kill -0 "$pid" 2>/dev/null; do
	now="$(date +%s)"
	[ "$((now - started))" -lt "$duration" ] || break

	cpu_ticks="$(awk '{print $14 + $15}' "/proc/$pid/stat")"
	rss_kib="$(awk '/^VmRSS:/ {print $2}' "/proc/$pid/status")"
	vm_kib="$(awk '/^VmSize:/ {print $2}' "/proc/$pid/status")"
	threads="$(awk '/^Threads:/ {print $2}' "/proc/$pid/status")"
	printf '%s,%s,%s,%s,%s,%s\n' \
		"$now" "$pid" "$cpu_ticks" "$rss_kib" "$vm_kib" "$threads"
	sleep "$interval"
done
