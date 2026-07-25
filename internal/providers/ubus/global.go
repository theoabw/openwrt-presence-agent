package ubus

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/identity"
	"github.com/theoabw/openwrt-presence-agent/internal/observation"
)

const (
	runtimeDirectory = "/tmp/openwrt-presence-agent"
)

type eventSubscriber interface {
	Subscribe(context.Context, []string, uint64, chan<- subscriptionEvent) error
}

type globalSubscriber struct {
	socketPath        string
	runtimeDir        string
	maxEventBytes     int
	keepaliveInterval time.Duration
	counter           atomic.Uint64
}

func newGlobalSubscriber(socketPath string, maxEventBytes int) *globalSubscriber {
	return &globalSubscriber{
		socketPath: socketPath, runtimeDir: runtimeDirectory,
		maxEventBytes: maxEventBytes, keepaliveInterval: 5 * time.Second,
	}
}

func (s *globalSubscriber) Subscribe(ctx context.Context, objects []string, generation uint64, events chan<- subscriptionEvent) error {
	if err := os.MkdirAll(s.runtimeDir, 0o700); err != nil {
		return fmt.Errorf("create hostapd runtime directory: %w", err)
	}
	localPath := filepath.Join(s.runtimeDir, fmt.Sprintf("event-%d-%d.sock", os.Getpid(), s.counter.Add(1)))
	local := &net.UnixAddr{Name: localPath, Net: "unixgram"}
	remote := &net.UnixAddr{Name: s.socketPath, Net: "unixgram"}
	conn, err := net.DialUnix("unixgram", local, remote)
	if err != nil {
		return fmt.Errorf("connect hostapd global control socket: %w", err)
	}
	defer conn.Close()
	defer os.Remove(localPath)
	if err := os.Chmod(localPath, 0o600); err != nil {
		return fmt.Errorf("secure hostapd event socket: %w", err)
	}
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	if _, err := conn.Write([]byte("ATTACH")); err != nil {
		return fmt.Errorf("attach hostapd event socket: %w", err)
	}
	buffer := make([]byte, s.maxEventBytes)
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return err
	}
	n, _, flags, _, err := conn.ReadMsgUnix(buffer, nil)
	if err != nil {
		return fmt.Errorf("read hostapd attach response: %w", err)
	}
	if flags&syscall.MSG_TRUNC != 0 {
		return fmt.Errorf("hostapd attach response exceeded configured limit")
	}
	if strings.TrimSpace(string(buffer[:n])) != "OK" {
		return fmt.Errorf("hostapd rejected event attachment")
	}

	known := make(map[string]struct{}, len(objects))
	for _, object := range objects {
		known[object] = struct{}{}
	}
	lastKeepalive := time.Now()
	for {
		readDeadline := time.Second
		if s.keepaliveInterval < readDeadline {
			readDeadline = s.keepaliveInterval
		}
		if err := conn.SetReadDeadline(time.Now().Add(readDeadline)); err != nil {
			return err
		}
		n, _, flags, _, err = conn.ReadMsgUnix(buffer, nil)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				if time.Since(lastKeepalive) >= s.keepaliveInterval {
					if err := conn.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
						return err
					}
					if _, err := conn.Write([]byte("PING")); err != nil {
						return fmt.Errorf("probe hostapd event socket: %w", err)
					}
					lastKeepalive = time.Now()
				}
				continue
			}
			return fmt.Errorf("read hostapd event: %w", err)
		}
		if flags&syscall.MSG_TRUNC != 0 {
			return fmt.Errorf("hostapd event exceeded configured limit")
		}
		value, relevant, err := parseHostapdEvent(buffer[:n], known, time.Now().UTC())
		if err != nil {
			return err
		}
		if !relevant {
			continue
		}
		select {
		case events <- subscriptionEvent{generation: generation, value: value}:
		default:
			return fmt.Errorf("provider event queue is full")
		}
	}
}

func parseHostapdEvent(data []byte, known map[string]struct{}, received time.Time) (observation.Observation, bool, error) {
	fields := strings.Fields(strings.TrimSpace(string(data)))
	if len(fields) < 3 || !strings.HasPrefix(fields[0], "IFNAME=") {
		return observation.Observation{}, false, nil
	}
	ifname := strings.TrimPrefix(fields[0], "IFNAME=")
	source := "hostapd." + ifname
	if _, ok := known[source]; !ok {
		return observation.Observation{}, false, nil
	}
	eventType := fields[1]
	if priorityEnd := strings.IndexByte(eventType, '>'); strings.HasPrefix(eventType, "<") && priorityEnd >= 0 {
		eventType = eventType[priorityEnd+1:]
	}
	var kind observation.Kind
	switch eventType {
	case "AP-STA-CONNECTED":
		kind = observation.WiFiAssociated
	case "AP-STA-DISCONNECTED":
		kind = observation.WiFiDisassociated
	default:
		return observation.Observation{}, false, nil
	}
	clientID, err := identity.ClientID(fields[2])
	if err != nil {
		return observation.Observation{}, false, fmt.Errorf("hostapd event has invalid client identifier")
	}
	return observation.Observation{
		Provider: providerID, SourceInstance: source, ReceivedAt: received,
		ClientID: clientID, Kind: kind, Confidence: observation.Authoritative,
	}, true, nil
}
