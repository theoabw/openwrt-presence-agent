package ubus

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/engine"
	"github.com/theoabw/openwrt-presence-agent/internal/observation"
	"github.com/theoabw/openwrt-presence-agent/pkg/protocol"
)

type fakeSubscriber struct {
	value observation.Observation
}

func testConfig(executable string) Config {
	return Config{
		UbusPath:          executable,
		DiscoveryInterval: time.Second,
		ReconcileInterval: time.Second,
		CommandTimeout:    time.Second,
		MaxCommandOutput:  1024 * 1024,
		MaxEventBytes:     64 * 1024,
		MaxClients:        10,
		QueueSize:         16,
	}
}

func (s fakeSubscriber) Subscribe(ctx context.Context, _ []string, generation uint64, events chan<- subscriptionEvent) error {
	select {
	case events <- subscriptionEvent{generation: generation, value: s.value}:
	case <-ctx.Done():
		return nil
	}
	<-ctx.Done()
	return nil
}

func TestDecodeSnapshot(t *testing.T) {
	data := []byte(`{"freq":5180,"clients":{
		"AA:BB:CC:DD:EE:FF":{"assoc":true,"authorized":true,"signal":-42},
		"00:00:00:00:00:01":{"assoc":true,"authorized":false},
		"00:00:00:00:00:02":{"assoc":false,"authorized":true},
		"00:00:00:00:00:03":{"assoc":true},
		"00:00:00:00:00:04":{"authorized":true},
		"invalid":{"assoc":true,"authorized":true}
	}}`)
	got, err := decodeSnapshot(data, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "mac:aa:bb:cc:dd:ee:ff" {
		t.Fatalf("decodeSnapshot() = %#v", got)
	}
}

func TestDecodeSnapshotBound(t *testing.T) {
	if _, err := decodeSnapshot([]byte(`{"clients":{"00:00:00:00:00:01":{},"00:00:00:00:00:02":{}}}`), 1); err == nil {
		t.Fatal("decodeSnapshot accepted excess clients")
	}
}

func TestDecodeSnapshotRequiresClientsMap(t *testing.T) {
	for _, data := range [][]byte{[]byte(`{}`), []byte(`{"clients":null}`), []byte(`{"clients":[]}`)} {
		if _, err := decodeSnapshot(data, 10); err == nil {
			t.Fatalf("decodeSnapshot accepted %s", data)
		}
	}
}

func TestProviderSnapshotAndImmediateEvent(t *testing.T) {
	executable, err := filepath.Abs("../../../testdata/fake-ubus.sh")
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.New(engine.Limits{MaxClients: 10, MaxSubscribers: 2, QueueSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(executable)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	started := time.Now()
	subscriber := fakeSubscriber{value: observation.Observation{
		Provider: providerID, SourceInstance: "hostapd.wlan1",
		ReceivedAt: time.Now().UTC(), ClientID: "mac:02:00:00:00:00:02",
		Kind: observation.WiFiAssociated, Confidence: observation.Authoritative,
	}}
	go func() {
		result <- newWithSubscriber(cfg, state, slog.New(slog.NewTextHandler(io.Discard, nil)), subscriber).Run(ctx)
	}()
	deadline := time.Now().Add(750 * time.Millisecond)
	for {
		snapshotClient, snapshotOK := state.Client("mac:02:00:00:00:00:01")
		eventClient, eventOK := state.Client("mac:02:00:00:00:00:02")
		if snapshotOK && eventOK {
			if snapshotClient.State != protocol.StatePresent || eventClient.State != protocol.StatePresent {
				t.Fatalf("clients are not present: %#v %#v", snapshotClient, eventClient)
			}
			if time.Since(started) >= time.Second {
				t.Fatalf("event delivery took %s", time.Since(started))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("provider did not publish snapshot and event: %#v", state.Snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not stop within bound")
	}
}

func TestEventReceivedDuringSnapshotIsAppliedAfterSnapshot(t *testing.T) {
	executable, err := filepath.Abs("../../../testdata/fake-ubus-race.sh")
	if err != nil {
		t.Fatal(err)
	}
	state, err := engine.New(engine.Limits{MaxClients: 10, MaxSubscribers: 2, QueueSize: 16})
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(executable)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	subscriber := fakeSubscriber{value: observation.Observation{
		Provider: providerID, SourceInstance: "hostapd.wlan0",
		ReceivedAt: time.Now().UTC(), ClientID: "mac:02:00:00:00:00:03",
		Kind: observation.WiFiAssociated, Confidence: observation.Authoritative,
	}}
	go func() {
		result <- newWithSubscriber(cfg, state, slog.New(slog.NewTextHandler(io.Discard, nil)), subscriber).Run(ctx)
	}()
	deadline := time.Now().Add(750 * time.Millisecond)
	for {
		client, ok := state.Client("mac:02:00:00:00:00:03")
		if ok && client.State == protocol.StatePresent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("event was lost across snapshot: %#v", state.Snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("provider did not stop within bound")
	}
}
