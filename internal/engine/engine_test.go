package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/theoabw/openwrt-presence-agent/pkg/protocol"
)

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(Limits{MaxClients: 10, MaxSubscribers: 2, QueueSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDisassociationIsConnectionScoped(t *testing.T) {
	e := newTestEngine(t)
	now := time.Now().UTC()
	if err := e.Associate("ubus", "hostapd.wlan0", "mac:00:11:22:33:44:55", now, "event"); err != nil {
		t.Fatal(err)
	}
	if err := e.Associate("ubus", "hostapd.wlan1", "mac:00:11:22:33:44:55", now, "event"); err != nil {
		t.Fatal(err)
	}
	e.Disassociate("ubus", "hostapd.wlan0", "mac:00:11:22:33:44:55", now, "event")
	got, _ := e.Client("mac:00:11:22:33:44:55")
	if got.State != protocol.StatePresent || len(got.Connections) != 1 {
		t.Fatalf("client after scoped disconnect = %#v", got)
	}
}

func TestProviderFailureCreatesUncertainty(t *testing.T) {
	e := newTestEngine(t)
	now := time.Now().UTC()
	_ = e.Associate("ubus", "hostapd.wlan0", "mac:00:11:22:33:44:55", now, "event")
	e.SetProvider(protocol.Provider{ID: "ubus", Status: "unavailable"})
	got, _ := e.Client("mac:00:11:22:33:44:55")
	if got.State != protocol.StateUnknown || got.Present != nil {
		t.Fatalf("client after provider failure = %#v", got)
	}
}

func TestReconcileCorrectsMissedEvents(t *testing.T) {
	e := newTestEngine(t)
	now := time.Now().UTC()
	_ = e.Associate("ubus", "hostapd.wlan0", "mac:00:11:22:33:44:55", now, "event")
	if err := e.Reconcile("ubus", map[string][]string{"hostapd.wlan0": nil}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Client("mac:00:11:22:33:44:55")
	if got.State != protocol.StateAbsent || got.Present == nil || *got.Present {
		t.Fatalf("client after reconciliation = %#v", got)
	}
}

func TestLastDisassociationWaitsForAuthoritativeSnapshot(t *testing.T) {
	e := newTestEngine(t)
	now := time.Now().UTC()
	_ = e.Associate("ubus", "hostapd.wlan0", "mac:00:11:22:33:44:55", now, "event")
	e.Disassociate("ubus", "hostapd.wlan0", "mac:00:11:22:33:44:55", now, "event")
	got, _ := e.Client("mac:00:11:22:33:44:55")
	if got.State != protocol.StateUnknown || got.Present != nil {
		t.Fatalf("client before authoritative snapshot = %#v", got)
	}
	if err := e.Reconcile("ubus", map[string][]string{"hostapd.wlan0": nil}, now); err != nil {
		t.Fatal(err)
	}
	got, _ = e.Client("mac:00:11:22:33:44:55")
	if got.State != protocol.StateAbsent || got.Present == nil || *got.Present {
		t.Fatalf("client after authoritative snapshot = %#v", got)
	}
}

func TestSourceReconcileOnlyReplacesAffectedBSS(t *testing.T) {
	e := newTestEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	if err := e.Reconcile("ubus", map[string][]string{
		"hostapd.wlan0": {clientID},
		"hostapd.wlan1": {clientID},
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := e.ReconcileSource(
		"ubus", "hostapd.wlan0", nil, now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Client(clientID)
	if got.State != protocol.StatePresent || len(got.Connections) != 1 {
		t.Fatalf("client after source reconciliation = %#v", got)
	}
	if got.Connections[0].SourceInstance != "hostapd.wlan1" {
		t.Fatalf("remaining connection = %#v", got.Connections[0])
	}
	if err := e.ReconcileSource(
		"ubus", "hostapd.wlan1", nil, now.Add(2*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	got, _ = e.Client(clientID)
	if got.State != protocol.StateAbsent || got.Present == nil || *got.Present {
		t.Fatalf("client after final source reconciliation = %#v", got)
	}
}

func TestSubscribeSnapshotDoesNotRaceEvents(t *testing.T) {
	e := newTestEngine(t)
	snapshot, events, cancel, err := e.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := e.Associate("ubus", "hostapd.wlan0", "mac:00:11:22:33:44:55", time.Now(), "event"); err != nil {
		t.Fatal(err)
	}
	event := <-events
	if event.Sequence != snapshot.Sequence+1 {
		t.Fatalf("first event sequence = %d, snapshot = %d", event.Sequence, snapshot.Sequence)
	}
}

func TestSlowSubscriberIsDisconnected(t *testing.T) {
	e, err := New(Limits{MaxClients: 10, MaxSubscribers: 1, QueueSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, events, cancel, err := e.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	_ = e.Associate("ubus", "hostapd.wlan0", "mac:00:11:22:33:44:55", time.Now(), "event")
	_ = e.Associate("ubus", "hostapd.wlan1", "mac:00:11:22:33:44:55", time.Now(), "event")
	for range events {
	}
	if got := e.Stats()["dropped_stream_clients"]; got != uint64(1) {
		t.Fatalf("dropped stream clients = %v", got)
	}
}

func TestThousandClientSnapshotIsBoundedAndConsistent(t *testing.T) {
	e, err := New(Limits{MaxClients: 1000, MaxSubscribers: 1, QueueSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	stations := map[string][]string{"hostapd.wlan0": make([]string, 1000)}
	for i := range stations["hostapd.wlan0"] {
		stations["hostapd.wlan0"][i] = fmt.Sprintf("mac:02:00:00:%02x:%02x:%02x", i>>16, i>>8, i)
	}
	if err := e.Reconcile("ubus", stations, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	snapshot := e.Snapshot()
	if len(snapshot.Clients) != 1000 || snapshot.Sequence == 0 {
		t.Fatalf("snapshot clients=%d sequence=%d", len(snapshot.Clients), snapshot.Sequence)
	}
}

func TestAbsentClientIsEvictedAtLimit(t *testing.T) {
	e, err := New(Limits{MaxClients: 1, MaxSubscribers: 1, QueueSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := e.Reconcile("ubus", map[string][]string{
		"hostapd.wlan0": {"mac:02:00:00:00:00:01"},
	}, now); err != nil {
		t.Fatal(err)
	}
	if err := e.Reconcile("ubus", map[string][]string{"hostapd.wlan0": nil}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := e.Associate("ubus", "hostapd.wlan0", "mac:02:00:00:00:00:02", now.Add(2*time.Second), "event"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Client("mac:02:00:00:00:00:01"); ok {
		t.Fatal("old absent client was retained")
	}
	if _, ok := e.Client("mac:02:00:00:00:00:02"); !ok {
		t.Fatal("new client was not admitted")
	}
}

func BenchmarkReconcile(b *testing.B) {
	for _, clients := range []int{50, 500} {
		b.Run(fmt.Sprintf("clients_%d", clients), func(b *testing.B) {
			stations := map[string][]string{"hostapd.wlan0": make([]string, clients)}
			for i := range stations["hostapd.wlan0"] {
				stations["hostapd.wlan0"][i] = fmt.Sprintf(
					"mac:02:00:00:%02x:%02x:%02x", i>>16, i>>8, i,
				)
			}
			e, err := New(Limits{
				MaxClients: clients, MaxSubscribers: 1, QueueSize: 1,
			})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				if err := e.Reconcile("ubus", stations, time.Now()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkReconcileSource(b *testing.B) {
	const clients = 500
	stations := map[string][]string{"hostapd.wlan0": make([]string, clients)}
	for i := range stations["hostapd.wlan0"] {
		stations["hostapd.wlan0"][i] = fmt.Sprintf(
			"mac:02:00:00:%02x:%02x:%02x", i>>16, i>>8, i,
		)
	}
	e, err := New(Limits{
		MaxClients: clients, MaxSubscribers: 1, QueueSize: 1,
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := e.Reconcile("ubus", stations, time.Now()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := e.ReconcileSource(
			"ubus", "hostapd.wlan0", stations["hostapd.wlan0"], time.Now(),
		); err != nil {
			b.Fatal(err)
		}
	}
}
