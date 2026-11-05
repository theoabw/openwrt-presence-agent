//go:build linux

package mdns

import (
	"context"
	"fmt"
	"net"
	"syscall"
)

const multicastPort = 5353

// openSocket binds the shared mDNS port and joins the multicast group on
// exactly one interface, so queries received on other interfaces are never
// delivered to this responder.
func openSocket(interfaceName string) (*net.UDPConn, error) {
	iface, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return nil, fmt.Errorf("resolve mDNS interface: %w", err)
	}
	address, ok := firstIPv4(iface, true)
	if !ok {
		return nil, fmt.Errorf("interface %s has no IPv4 address", interfaceName)
	}
	listenConfig := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var controlErr error
		if err := raw.Control(func(fd uintptr) {
			controlErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		}); err != nil {
			return err
		}
		return controlErr
	}}
	packetConn, err := listenConfig.ListenPacket(
		context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", multicastPort),
	)
	if err != nil {
		return nil, fmt.Errorf("bind mDNS socket: %w", err)
	}
	conn, ok := packetConn.(*net.UDPConn)
	if !ok {
		_ = packetConn.Close()
		return nil, fmt.Errorf("mDNS socket is not UDP")
	}
	if err := configureMulticast(conn, iface, address); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func configureMulticast(conn *net.UDPConn, iface *net.Interface, address net.IP) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("access mDNS socket: %w", err)
	}
	var configureErr error
	if err := raw.Control(func(fd uintptr) {
		socket := int(fd)
		membership := &syscall.IPMreqn{
			Multiaddr: [4]byte{224, 0, 0, 251},
			Ifindex:   int32(iface.Index),
		}
		if err := syscall.SetsockoptIPMreqn(socket, syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, membership); err != nil {
			configureErr = fmt.Errorf("join mDNS group on %s: %w", iface.Name, err)
			return
		}
		var local [4]byte
		copy(local[:], address.To4())
		if err := syscall.SetsockoptInet4Addr(socket, syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, local); err != nil {
			configureErr = fmt.Errorf("select mDNS interface %s: %w", iface.Name, err)
			return
		}
		// TTL 1 keeps announcements on the local link, and loopback lets local
		// consumers and the integration test observe the same packets.
		if err := syscall.SetsockoptInt(socket, syscall.IPPROTO_IP, syscall.IP_MULTICAST_TTL, 1); err != nil {
			configureErr = fmt.Errorf("set mDNS multicast TTL: %w", err)
			return
		}
		if err := syscall.SetsockoptInt(socket, syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, 1); err != nil {
			configureErr = fmt.Errorf("enable mDNS loopback: %w", err)
		}
	}); err != nil {
		return fmt.Errorf("configure mDNS socket: %w", err)
	}
	return configureErr
}
