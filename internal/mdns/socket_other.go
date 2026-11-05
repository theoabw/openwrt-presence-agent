//go:build !linux

package mdns

import (
	"errors"
	"net"
)

var errUnavailable = errors.New("mDNS discovery is unavailable on this platform")

func openSocket(string) (*net.UDPConn, error) {
	return nil, errUnavailable
}
