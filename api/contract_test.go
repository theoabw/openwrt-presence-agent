package api_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/theoabw/openwrt-presence-agent/pkg/protocol"
)

type contractFixture struct {
	FixtureVersion  int               `json:"fixture_version"`
	ProtocolVersion string            `json:"protocol_version"`
	Info            infoFixture       `json:"info"`
	Snapshot        protocol.Snapshot `json:"snapshot"`
	Events          []protocol.Event  `json:"events"`
}

type infoFixture struct {
	Name            string   `json:"name"`
	ProtocolVersion string   `json:"protocol_version"`
	AgentID         string   `json:"agent_id"`
	Version         string   `json:"version"`
	Capabilities    []string `json:"capabilities"`
}

func TestV1ContractFixtureMatchesPublicTypes(t *testing.T) {
	data, err := os.ReadFile("fixtures/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture contractFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.FixtureVersion != 1 || fixture.ProtocolVersion != protocol.Version {
		t.Fatalf("fixture versions = %d, %q", fixture.FixtureVersion, fixture.ProtocolVersion)
	}
	if fixture.Info.Name != "openwrt-presence-agent" ||
		fixture.Info.ProtocolVersion != protocol.Version ||
		len(fixture.Info.AgentID) != 32 ||
		fixture.Info.Version == "" {
		t.Fatalf("invalid info fixture: %#v", fixture.Info)
	}
	required := map[string]bool{
		"wifi_snapshot": false,
		"wifi_events":   false,
		"websocket":     false,
	}
	for _, capability := range fixture.Info.Capabilities {
		if _, ok := required[capability]; ok {
			required[capability] = true
		}
	}
	for capability, present := range required {
		if !present {
			t.Fatalf("fixture missing capability %q", capability)
		}
	}
	if len(fixture.Snapshot.Clients) != 1 || len(fixture.Events) != 3 {
		t.Fatalf("fixture shape = %d clients, %d events", len(fixture.Snapshot.Clients), len(fixture.Events))
	}
	if fixture.Events[0].Type != "stream.hello" ||
		fixture.Events[1].Type != "state.snapshot" ||
		fixture.Events[2].Type != "client.presence_changed" {
		t.Fatalf("unexpected fixture events: %#v", fixture.Events)
	}
	if _, err := json.Marshal(fixture); err != nil {
		t.Fatal(err)
	}
}
