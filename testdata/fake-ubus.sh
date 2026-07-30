#!/bin/sh
set -eu

case "$1" in
	list)
		printf '%s\n' hostapd.wlan0 hostapd.wlan1
		;;
	call)
		case "$2" in
			hostapd.wlan0)
				printf '%s\n' '{"freq":2412,"clients":{"02:00:00:00:00:01":{"assoc":true,"authorized":true,"signal":-40}}}'
				;;
			hostapd.wlan1)
				printf '%s\n' '{"freq":5180,"clients":{}}'
				;;
			*)
				exit 1
				;;
		esac
		;;
	subscribe)
		printf '%s\n' '{"hostapd.wlan1":{"assoc":{"address":"02:00:00:00:00:02"}}}'
		while :; do
			sleep 10
		done
		;;
	*)
		exit 1
		;;
esac
