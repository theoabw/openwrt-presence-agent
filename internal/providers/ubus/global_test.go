package ubus

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/observation"
)

func TestParseHostapdGlobalEvents(t *testing.T) {
	t.Parallel()
	known := map[string]struct{}{"hostapd.wlan1": {}}
	for _, test := range []struct {
		name string
		line string
		kind observation.Kind
	}{
		{"connected", "IFNAME=wlan1 <3>AP-STA-CONNECTED 02:00:00:00:00:01", observation.WiFiAssociated},
		{"disconnected", "IFNAME=wlan1 <3>AP-STA-DISCONNECTED 02:00:00:00:00:01", observation.WiFiDisassociated},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, relevant, err := parseHostapdEvent([]byte(test.line), known, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if !relevant || got.Kind != test.kind || got.SourceInstance != "hostapd.wlan1" {
				t.Fatalf("parseHostapdEvent() = %#v, %t", got, relevant)
			}
		})
	}
}

func TestParseHostapdEventIgnoresUnknownSourceAndType(t *testing.T) {
	t.Parallel()
	known := map[string]struct{}{"hostapd.wlan1": {}}
	for _, line := range []string{
		"IFNAME=wlan2 <3>AP-STA-CONNECTED 02:00:00:00:00:01",
		"IFNAME=wlan1 <3>EAPOL-4WAY-HS-COMPLETED 02:00:00:00:00:01",
		"garbage",
	} {
		if _, relevant, err := parseHostapdEvent([]byte(line), known, time.Now().UTC()); err != nil || relevant {
			t.Fatalf("parseHostapdEvent(%q) relevant=%t err=%v", line, relevant, err)
		}
	}
}

func TestGlobalSubscriberAttachAndEvent(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	remotePath := filepath.Join(temp, "global")
	server, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: remotePath, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	subscriber := &globalSubscriber{
		socketPath: remotePath, runtimeDir: filepath.Join(temp, "runtime"),
		maxEventBytes: 4096, keepaliveInterval: time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan subscriptionEvent, 1)
	result := make(chan error, 1)
	go func() {
		result <- subscriber.Subscribe(ctx, []string{"hostapd.wlan1"}, 7, events)
	}()
	buffer := make([]byte, 128)
	n, client, err := server.ReadFromUnix(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "ATTACH" {
		t.Fatalf("attach command = %q", buffer[:n])
	}
	if _, err := server.WriteToUnix([]byte("OK\n"), client); err != nil {
		t.Fatal(err)
	}
	if _, err := server.WriteToUnix([]byte("IFNAME=wlan1 <3>AP-STA-CONNECTED 02:00:00:00:00:01"), client); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.generation != 7 || event.value.Kind != observation.WiFiAssociated {
			t.Fatalf("event = %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("event was not delivered")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber did not stop")
	}
}

func TestGlobalSubscriberDetectsSocketDisappearance(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	remotePath := filepath.Join(temp, "global")
	server, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: remotePath, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	subscriber := &globalSubscriber{
		socketPath: remotePath, runtimeDir: filepath.Join(temp, "runtime"),
		maxEventBytes: 4096, keepaliveInterval: 20 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- subscriber.Subscribe(ctx, []string{"hostapd.wlan1"}, 1, make(chan subscriptionEvent, 1))
	}()
	buffer := make([]byte, 128)
	n, client, err := server.ReadFromUnix(buffer)
	if err != nil {
		t.Fatal(err)
	}
	if string(buffer[:n]) != "ATTACH" {
		t.Fatalf("attach command = %q", buffer[:n])
	}
	if _, err := server.WriteToUnix([]byte("OK\n"), client); err != nil {
		t.Fatal(err)
	}
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("socket disappearance was not reported")
		}
	case <-time.After(time.Second):
		t.Fatal("socket disappearance was not detected")
	}
}
