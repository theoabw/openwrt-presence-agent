// Package observation defines provider-independent input to the state engine.
package observation

import "time"

type Kind string

const (
	WiFiAssociated    Kind = "wifi.associated"
	WiFiDisassociated Kind = "wifi.disassociated"
)

type Confidence string

const (
	Authoritative Confidence = "authoritative"
)

type Observation struct {
	Provider       string
	SourceInstance string
	ObservedAt     *time.Time
	ReceivedAt     time.Time
	ClientID       string
	Kind           Kind
	Confidence     Confidence
}

type Snapshot struct {
	Provider   string
	ReceivedAt time.Time
	Stations   map[string][]string
}

// SourceSnapshot authoritatively replaces one provider source instance.
type SourceSnapshot struct {
	Provider       string
	SourceInstance string
	ReceivedAt     time.Time
	Clients        []string
}

type ProviderStatus struct {
	ID                string
	Kind              string
	Status            string
	SnapshotSupported bool
	Sources           []string
	LastSnapshotAt    time.Time
	LastEventAt       time.Time
	LastError         string
	SnapshotSource    string
	EventSource       string
}

type Sink interface {
	Apply(Observation) error
	ApplySnapshot(Snapshot) error
	ApplySourceSnapshot(SourceSnapshot) error
	SetProviderStatus(ProviderStatus)
}
