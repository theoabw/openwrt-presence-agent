//go:build linux

package wired

import (
	"encoding/binary"
	"syscall"
	"testing"
)

func TestDecodeReachableNeighbor(t *testing.T) {
	data := make([]byte, ndmsgSize+12)
	data[0] = syscall.AF_INET
	binary.NativeEndian.PutUint32(data[4:8], 7)
	binary.NativeEndian.PutUint16(data[8:10], nudReachable)
	binary.NativeEndian.PutUint16(data[12:14], 10)
	binary.NativeEndian.PutUint16(data[14:16], ndaLLAddr)
	copy(data[16:22], []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55})

	clientID, ifindex, ok := decodeReachableNeighbor(syscall.NetlinkMessage{
		Header: syscall.NlMsghdr{Type: syscall.RTM_NEWNEIGH},
		Data:   data,
	})
	if !ok || clientID != "mac:00:11:22:33:44:55" || ifindex != 7 {
		t.Fatalf("decodeReachableNeighbor() = %q, %d, %v", clientID, ifindex, ok)
	}
}

func TestDecodeNeighborRejectsStaleAndBridgeEntries(t *testing.T) {
	for _, family := range []byte{syscall.AF_INET, syscall.AF_BRIDGE} {
		data := make([]byte, ndmsgSize+12)
		data[0] = family
		binary.NativeEndian.PutUint32(data[4:8], 7)
		// A zero state is not fresh reachability evidence. AF_BRIDGE entries
		// are forwarding state rather than confirmed IP neighbors.
		binary.NativeEndian.PutUint16(data[12:14], 10)
		binary.NativeEndian.PutUint16(data[14:16], ndaLLAddr)
		copy(data[16:22], []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55})
		if _, _, ok := decodeReachableNeighbor(syscall.NetlinkMessage{
			Header: syscall.NlMsghdr{Type: syscall.RTM_NEWNEIGH},
			Data:   data,
		}); ok {
			t.Fatalf("accepted family=%d state=0 neighbor", family)
		}
	}
}
