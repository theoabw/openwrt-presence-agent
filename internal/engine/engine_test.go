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

func newGraceEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := New(Limits{
		MaxClients: 10, MaxSubscribers: 2, QueueSize: 8,
		DepartureDelay: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Stop)
	return e
}

func assertNoClientEvents(t *testing.T, events <-chan protocol.Event) {
	t.Helper()
	for {
		select {
		case ev := <-events:
			if ev.Type != "state.resynchronized" {
				t.Fatalf("unexpected client event during hold: %s", ev.Type)
			}
		default:
			return
		}
	}
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

func TestHealthyProviderSnapshotDoesNotResurrectStaleConnection(t *testing.T) {
	e := newTestEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	_ = e.Associate("ubus", "hostapd.wlan0", clientID, now, "event")
	e.SetProvider(protocol.Provider{ID: "ubus", Status: "unavailable"})

	if err := e.Reconcile(
		"wired", map[string][]string{"br-lan": nil}, now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Client(clientID)
	if got.State != protocol.StateUnknown || got.Present != nil {
		t.Fatalf("client with only stale connections = %#v", got)
	}
}

func TestProviderFailureRetainsPresenceFromHealthyProvider(t *testing.T) {
	e := newTestEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	_ = e.Associate("ubus", "hostapd.wlan0", clientID, now, "event")
	_ = e.Associate("wired", "br-lan", clientID, now, "event")

	e.SetProvider(protocol.Provider{ID: "ubus", Status: "unavailable"})
	got, _ := e.Client(clientID)
	if got.State != protocol.StatePresent || got.Present == nil || !*got.Present {
		t.Fatalf("client with a healthy connection = %#v", got)
	}
}

func TestProviderFailureDoesNotChangeUnrelatedAbsentClient(t *testing.T) {
	e := newTestEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	if err := e.Reconcile(
		"wired", map[string][]string{"br-lan": {clientID}}, now,
	); err != nil {
		t.Fatal(err)
	}
	if err := e.Reconcile(
		"wired", map[string][]string{"br-lan": nil}, now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}

	e.SetProvider(protocol.Provider{ID: "ubus", Status: "unavailable"})
	got, _ := e.Client(clientID)
	if got.State != protocol.StateAbsent || got.Present == nil || *got.Present {
		t.Fatalf("unrelated absent client after provider failure = %#v", got)
	}
}

func TestDisassociationDoesNotCountRemainingStaleConnection(t *testing.T) {
	e := newTestEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	_ = e.Associate("wired", "br-lan", clientID, now, "event")
	_ = e.Associate("ubus", "hostapd.wlan0", clientID, now, "event")
	e.SetProvider(protocol.Provider{ID: "wired", Status: "unavailable"})

	e.Disassociate("ubus", "hostapd.wlan0", clientID, now.Add(time.Second), "event")
	got, _ := e.Client(clientID)
	if got.State != protocol.StateUnknown || got.Present != nil {
		t.Fatalf("client with only a stale connection after departure = %#v", got)
	}
}

func TestSourceReconcileRecoversStaleConnection(t *testing.T) {
	e := newTestEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	_ = e.Associate("ubus", "hostapd.wlan0", clientID, now, "event")
	e.SetProvider(protocol.Provider{ID: "ubus", Status: "unavailable"})

	if err := e.ReconcileSource(
		"ubus", "hostapd.wlan0", []string{clientID}, now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Client(clientID)
	if got.State != protocol.StatePresent || got.Present == nil || !*got.Present {
		t.Fatalf("client after authoritative source recovery = %#v", got)
	}
	if len(got.Connections) != 1 || got.Connections[0].Stale {
		t.Fatalf("connection after authoritative source recovery = %#v", got.Connections)
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

func TestAllStaleClientIsEvictedAtLimit(t *testing.T) {
	e, err := New(Limits{MaxClients: 1, MaxSubscribers: 1, QueueSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first := "mac:02:00:00:00:00:01"
	second := "mac:02:00:00:00:00:02"
	if err := e.Associate("ubus", "hostapd.wlan0", first, now, "event"); err != nil {
		t.Fatal(err)
	}
	e.SetProvider(protocol.Provider{ID: "ubus", Status: "unavailable"})
	if err := e.Associate(
		"wired", "br-lan", second, now.Add(time.Second), "event",
	); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.Client(first); ok {
		t.Fatal("all-stale client was retained at the limit")
	}
	if _, ok := e.Client(second); !ok {
		t.Fatal("new live client was not admitted")
	}
}

func TestDepartureDelayHoldsLastDisassociation(t *testing.T) {
	e := newGraceEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	_ = e.Associate("ubus", "hostapd.wlan0", clientID, now, "event")
	snapshot, events, cancel, err := e.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	e.Disassociate("ubus", "hostapd.wlan0", clientID, now, "event")
	got, _ := e.Client(clientID)
	if got.State != protocol.StatePresent || got.Present == nil || !*got.Present {
		t.Fatalf("client during departure hold = %#v", got)
	}
	if len(got.Connections) != 1 {
		t.Fatalf("connection removed during hold = %#v", got.Connections)
	}
	select {
	case ev := <-events:
		t.Fatalf("unexpected event during hold: %s", ev.Type)
	default:
	}
	if e.Stats()["pending_departures"] != 1 {
		t.Fatalf("pending_departures = %v", e.Stats()["pending_departures"])
	}
	e.sweepPending(now.Add(30 * time.Second))
	got, _ = e.Client(clientID)
	if got.State != protocol.StateUnknown || got.Present != nil {
		t.Fatalf("client after grace elapsed = %#v", got)
	}
	if len(got.Connections) != 0 {
		t.Fatalf("connection after grace = %#v", got.Connections)
	}
	ev := <-events
	if ev.Type != "client.presence_changed" {
		t.Fatalf("event after grace = %s, want client.presence_changed", ev.Type)
	}
	if ev.Sequence != snapshot.Sequence+1 {
		t.Fatalf("event sequence = %d, want %d", ev.Sequence, snapshot.Sequence+1)
	}
	if e.Stats()["pending_departures"] != 0 {
		t.Fatalf("pending_departures after grace = %v", e.Stats()["pending_departures"])
	}
}

func TestDepartureDelayAbsorbsReconnectFlap(t *testing.T) {
	e := newGraceEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	_ = e.Associate("ubus", "hostapd.wlan0", clientID, now, "event")
	_, events, cancel, err := e.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	e.Disassociate("ubus", "hostapd.wlan0", clientID, now.Add(time.Second), "event")
	_ = e.Associate("ubus", "hostapd.wlan0", clientID, now.Add(2*time.Second), "event")
	e.sweepPending(now.Add(30 * time.Second))
	got, _ := e.Client(clientID)
	if got.State != protocol.StatePresent || got.Present == nil || !*got.Present {
		t.Fatalf("client after absorbed flap = %#v", got)
	}
	select {
	case ev := <-events:
		t.Fatalf("flap produced event: %s", ev.Type)
	default:
	}
	if e.Stats()["pending_departures"] != 0 {
		t.Fatalf("pending_departures after flap = %v", e.Stats()["pending_departures"])
	}
}

func TestDepartureDelayDoesNotHoldScopedDisassociation(t *testing.T) {
	e := newGraceEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	_ = e.Associate("ubus", "hostapd.wlan0", clientID, now, "event")
	_ = e.Associate("ubus", "hostapd.wlan1", clientID, now, "event")
	e.Disassociate("ubus", "hostapd.wlan0", clientID, now, "event")
	got, _ := e.Client(clientID)
	if got.State != protocol.StatePresent || len(got.Connections) != 1 {
		t.Fatalf("client after scoped disconnect with grace = %#v", got)
	}
	if e.Stats()["pending_departures"] != 0 {
		t.Fatalf("pending_departures = %v", e.Stats()["pending_departures"])
	}
}

func TestDepartureDelayReconcileHoldsAuthoritativeRemoval(t *testing.T) {
	e := newGraceEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	_ = e.Associate("ubus", "hostapd.wlan0", clientID, now, "event")
	_, events, cancel, err := e.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := e.Reconcile(
		"ubus", map[string][]string{"hostapd.wlan0": nil}, now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Client(clientID)
	if got.State != protocol.StatePresent {
		t.Fatalf("client during reconcile hold = %#v", got)
	}
	assertNoClientEvents(t, events)
	e.sweepPending(now.Add(30 * time.Second))
	got, _ = e.Client(clientID)
	if got.State != protocol.StateUnknown || got.Present != nil {
		t.Fatalf("client after reconcile grace = %#v", got)
	}
	if ev := <-events; ev.Type != "client.presence_changed" {
		t.Fatalf("event after reconcile grace = %s", ev.Type)
	}
}

func TestDepartureDelayReconcileConfirmsPresence(t *testing.T) {
	e := newGraceEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	_ = e.Associate("ubus", "hostapd.wlan0", clientID, now, "event")
	_, events, cancel, err := e.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	e.Disassociate("ubus", "hostapd.wlan0", clientID, now, "event")
	if err := e.Reconcile(
		"ubus", map[string][]string{"hostapd.wlan0": {clientID}}, now.Add(time.Second),
	); err != nil {
		t.Fatal(err)
	}
	e.sweepPending(now.Add(30 * time.Second))
	got, _ := e.Client(clientID)
	if got.State != protocol.StatePresent || got.Present == nil || !*got.Present {
		t.Fatalf("client after authoritative confirmation = %#v", got)
	}
	assertNoClientEvents(t, events)
	if e.Stats()["pending_departures"] != 0 {
		t.Fatalf("pending_departures = %v", e.Stats()["pending_departures"])
	}
}

func TestDepartureDelaySourceReconcileHolds(t *testing.T) {
	e := newGraceEngine(t)
	now := time.Now().UTC()
	clientID := "mac:00:11:22:33:44:55"
	_ = e.Associate("ubus", "hostapd.wlan0", clientID, now, "event")
	_, events, cancel, err := e.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if err := e.ReconcileSource("ubus", "hostapd.wlan0", nil, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got, _ := e.Client(clientID)
	if got.State != protocol.StatePresent {
		t.Fatalf("client during source reconcile hold = %#v", got)
	}
	assertNoClientEvents(t, events)
	e.sweepPending(now.Add(30 * time.Second))
	got, _ = e.Client(clientID)
	if got.State != protocol.StateUnknown || got.Present != nil {
		t.Fatalf("client after source reconcile grace = %#v", got)
	}
	if ev := <-events; ev.Type != "client.presence_changed" {
		t.Fatalf("event after source reconcile grace = %s", ev.Type)
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
