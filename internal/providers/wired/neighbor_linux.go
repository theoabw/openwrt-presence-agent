//go:build linux

package wired

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/identity"
)

const (
	rtmgrpNeigh  = 0x4
	ndaLLAddr    = 0x2
	nudReachable = 0x2
	ndmsgSize    = 12
	rtattrSize   = 4
)

type neighborEvent struct {
	clientID string
	at       time.Time
}

func listenNeighborEvents(ctx context.Context, interfaceName string, events chan<- neighborEvent) error {
	device, err := net.InterfaceByName(interfaceName)
	if err != nil {
		return fmt.Errorf("resolve LAN interface: %w", err)
	}
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("open route netlink socket: %w", err)
	}
	defer syscall.Close(fd)
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{
		Family: syscall.AF_NETLINK, Groups: rtmgrpNeigh,
	}); err != nil {
		return fmt.Errorf("subscribe to neighbor events: %w", err)
	}
	timeout := syscall.NsecToTimeval(time.Second.Nanoseconds())
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &timeout); err != nil {
		return fmt.Errorf("set netlink receive timeout: %w", err)
	}

	buffer := make([]byte, 64*1024)
	for ctx.Err() == nil {
		count, _, err := syscall.Recvfrom(fd, buffer, 0)
		if err != nil {
			if err == syscall.EAGAIN || err == syscall.EINTR {
				continue
			}
			return fmt.Errorf("receive neighbor event: %w", err)
		}
		messages, err := syscall.ParseNetlinkMessage(buffer[:count])
		if err != nil {
			return fmt.Errorf("parse neighbor event: %w", err)
		}
		for _, message := range messages {
			clientID, ifindex, ok := decodeReachableNeighbor(message)
			if !ok || ifindex != device.Index {
				continue
			}
			select {
			case events <- neighborEvent{clientID: clientID, at: time.Now().UTC()}:
			case <-ctx.Done():
				return nil
			}
		}
	}
	return nil
}

func decodeReachableNeighbor(message syscall.NetlinkMessage) (string, int, bool) {
	if message.Header.Type != syscall.RTM_NEWNEIGH || len(message.Data) < ndmsgSize {
		return "", 0, false
	}
	family := message.Data[0]
	if family != syscall.AF_INET && family != syscall.AF_INET6 {
		return "", 0, false
	}
	ifindex := int(int32(binary.NativeEndian.Uint32(message.Data[4:8])))
	state := binary.NativeEndian.Uint16(message.Data[8:10])
	if state&nudReachable == 0 {
		return "", 0, false
	}
	for attributes := message.Data[ndmsgSize:]; len(attributes) >= rtattrSize; {
		length := int(binary.NativeEndian.Uint16(attributes[:2]))
		kind := binary.NativeEndian.Uint16(attributes[2:4])
		if length < rtattrSize || length > len(attributes) {
			return "", 0, false
		}
		if kind == ndaLLAddr {
			clientID, err := identity.ClientID(net.HardwareAddr(attributes[rtattrSize:length]).String())
			return clientID, ifindex, err == nil
		}
		aligned := (length + 3) &^ 3
		if aligned > len(attributes) {
			return "", 0, false
		}
		attributes = attributes[aligned:]
	}
	return "", 0, false
}
