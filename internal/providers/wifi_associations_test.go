package providers

import (
	"testing"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/engine"
	"github.com/theoabw/openwrt-presence-agent/internal/observation"
)

func TestWiFiAssociationsTrackRoamingAndSnapshots(t *testing.T) {
	state, err := engine.New(engine.Limits{MaxClients: 10, MaxSubscribers: 1, QueueSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	associations := newWiFiAssociations(state)
	select {
	case <-associations.Ready():
		t.Fatal("association inventory was ready before its first snapshot")
	default:
	}
	clientID := "mac:00:11:22:33:44:55"
	at := time.Now().UTC()
	if err := associations.ApplySnapshot(observation.Snapshot{
		Provider: "ubus-hostapd", ReceivedAt: at,
		Stations: map[string][]string{
			"hostapd.wlan0": {clientID},
			"hostapd.wlan1": {clientID},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if !associations.Contains(clientID) {
		t.Fatal("snapshot client is not excluded from wired probes")
	}
	select {
	case <-associations.Ready():
	default:
		t.Fatal("association inventory was not ready after its first snapshot")
	}
	if err := associations.ApplySourceSnapshot(observation.SourceSnapshot{
		Provider: "ubus-hostapd", SourceInstance: "hostapd.wlan0",
		ReceivedAt: at, Clients: nil,
	}); err != nil {
		t.Fatal(err)
	}
	if !associations.Contains(clientID) {
		t.Fatal("client roaming on another BSS was removed")
	}
	if err := associations.ApplySourceSnapshot(observation.SourceSnapshot{
		Provider: "ubus-hostapd", SourceInstance: "hostapd.wlan1",
		ReceivedAt: at, Clients: nil,
	}); err != nil {
		t.Fatal(err)
	}
	if associations.Contains(clientID) {
		t.Fatal("client remained excluded after its final Wi-Fi connection disappeared")
	}
}

func TestWiFiAssociationsBecomeReadyWhenProviderIsUnavailable(t *testing.T) {
	state, err := engine.New(engine.Limits{MaxClients: 10, MaxSubscribers: 1, QueueSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	associations := newWiFiAssociations(state)
	associations.SetProviderStatus(observation.ProviderStatus{
		ID: "ubus-hostapd", Status: "unavailable",
	})
	select {
	case <-associations.Ready():
	default:
		t.Fatal("unavailable Wi-Fi provider left Ethernet gated")
	}
}
