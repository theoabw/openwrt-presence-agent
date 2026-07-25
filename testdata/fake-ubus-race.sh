#!/bin/sh
set -eu

case "$1" in
	list)
		printf '%s\n' hostapd.wlan0
		;;
	call)
		sleep 0.2
		printf '%s\n' '{"freq":2412,"clients":{}}'
		;;
	subscribe)
		printf '%s\n' '{"hostapd.wlan0":{"assoc":{"address":"02:00:00:00:00:03"}}}'
		while :; do
			sleep 10
		done
		;;
	*)
		exit 1
		;;
esac
