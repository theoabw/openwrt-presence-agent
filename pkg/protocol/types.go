// Package protocol contains the stable, provider-independent public protocol.
package protocol

import "time"

const Version = "v1"

type PresenceState string

const (
	StatePresent PresenceState = "present"
	StateAbsent  PresenceState = "absent"
	StateUnknown PresenceState = "unknown"
)

type Connection struct {
	ID             string    `json:"id"`
	Provider       string    `json:"provider"`
	SourceInstance string    `json:"source_instance"`
	ConnectedAt    time.Time `json:"connected_at"`
	LastSeenAt     time.Time `json:"last_seen_at"`
	Stale          bool      `json:"stale,omitempty"`
}

type Client struct {
	ID          string        `json:"id"`
	State       PresenceState `json:"state"`
	Present     *bool         `json:"present"`
	Connections []Connection  `json:"connections"`
	FirstSeenAt time.Time     `json:"first_seen_at"`
	LastSeenAt  time.Time     `json:"last_seen_at"`
}

type Snapshot struct {
	StreamEpoch string    `json:"stream_epoch"`
	Sequence    uint64    `json:"sequence"`
	GeneratedAt time.Time `json:"generated_at"`
	Clients     []Client  `json:"clients"`
}

type Event struct {
	Type        string    `json:"type"`
	EventID     string    `json:"event_id"`
	StreamEpoch string    `json:"stream_epoch"`
	Sequence    uint64    `json:"sequence"`
	Timestamp   time.Time `json:"timestamp"`
	Reason      string    `json:"reason,omitempty"`
	Data        any       `json:"data,omitempty"`
}

type Provider struct {
	ID                string    `json:"id"`
	Kind              string    `json:"kind"`
	Status            string    `json:"status"`
	SnapshotSupported bool      `json:"snapshot_supported"`
	Sources           []string  `json:"sources"`
	LastSnapshotAt    time.Time `json:"last_snapshot_at,omitempty"`
	LastEventAt       time.Time `json:"last_event_at,omitempty"`
	LastError         string    `json:"last_error,omitempty"`
	SnapshotSource    string    `json:"snapshot_source"`
	EventSource       string    `json:"event_source"`
}
