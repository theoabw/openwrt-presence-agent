// Package mdns advertises the agent API over multicast DNS/DNS-SD without
// external dependencies.
package mdns

import (
	"errors"
	"fmt"
	"net"
)

// ErrLoopbackOnly reports that the API listens on loopback only, so the agent
// must not advertise an address that any consumer could reach.
var ErrLoopbackOnly = errors.New("API listens on loopback only")

// ErrNoIPv4 reports that the API listens on a concrete IPv6 address only. The
// published DNS-SD record set carries an A record and no AAAA record, so no
// address the consumer could resolve is reachable. Advertising the interface
// IPv4 instead would publish an address the API is not listening on.
var ErrNoIPv4 = errors.New("API does not listen on an IPv4 address")

// advertisedAddress returns the IPv4 address the API is reachable on. A
// concrete non-loopback IPv4 listen address wins. A wildcard listen address
// falls back to the mDNS interface because the API accepts IPv4 connections on
// every interface, while a concrete IPv6 address has no IPv4 equivalent.
func advertisedAddress(listenAddress, interfaceName string) (net.IP, error) {
	parsed := net.ParseIP(listenAddress)
	if parsed == nil {
		return nil, errors.New("listen address is not a literal IP address")
	}
	if parsed.IsLoopback() {
		return nil, ErrLoopbackOnly
	}
	if v4 := parsed.To4(); v4 != nil {
		if v4.IsUnspecified() {
			return interfaceIPv4(interfaceName)
		}
		return v4, nil
	}
	if !parsed.IsUnspecified() {
		return nil, ErrNoIPv4
	}
	return interfaceIPv4(interfaceName)
}

func interfaceIPv4(name string) (net.IP, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("resolve mDNS interface: %w", err)
	}
	address, ok := firstIPv4(iface, false)
	if !ok {
		return nil, fmt.Errorf("interface %s has no non-loopback IPv4 address", name)
	}
	return address, nil
}

func firstIPv4(iface *net.Interface, includeLoopback bool) (net.IP, bool) {
	addresses, err := iface.Addrs()
	if err != nil {
		return nil, false
	}
	for _, address := range addresses {
		network, ok := address.(*net.IPNet)
		if !ok {
			continue
		}
		v4 := network.IP.To4()
		if v4 == nil || (!includeLoopback && v4.IsLoopback()) {
			continue
		}
		return v4, true
	}
	return nil, false
}
