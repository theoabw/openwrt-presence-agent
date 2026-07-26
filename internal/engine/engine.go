package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/observation"
	"github.com/theoabw/openwrt-presence-agent/pkg/protocol"
)

type Limits struct {
	MaxClients     int
	MaxSubscribers int
	QueueSize      int
}

func (e *Engine) Apply(value observation.Observation) error {
	switch value.Kind {
	case observation.WiFiAssociated, observation.WiredReachable:
		return e.Associate(value.Provider, value.SourceInstance, value.ClientID, value.ReceivedAt, "provider_event")
	case observation.WiFiDisassociated:
		e.Disassociate(value.Provider, value.SourceInstance, value.ClientID, value.ReceivedAt, "provider_event")
		return nil
	default:
		return fmt.Errorf("unsupported observation kind %q", value.Kind)
	}
}

func (e *Engine) ApplySnapshot(value observation.Snapshot) error {
	return e.Reconcile(value.Provider, value.Stations, value.ReceivedAt)
}

func (e *Engine) ApplySourceSnapshot(value observation.SourceSnapshot) error {
	return e.ReconcileSource(
		value.Provider,
		value.SourceInstance,
		value.Clients,
		value.ReceivedAt,
	)
}

func (e *Engine) SetProviderStatus(value observation.ProviderStatus) {
	e.SetProvider(protocol.Provider{
		ID: value.ID, Kind: value.Kind, Status: value.Status,
		SnapshotSupported: value.SnapshotSupported,
		Sources:           value.Sources,
		LastSnapshotAt:    value.LastSnapshotAt,
		LastEventAt:       value.LastEventAt,
		LastError:         value.LastError,
		SnapshotSource:    value.SnapshotSource,
		EventSource:       value.EventSource,
	})
}

type Engine struct {
	mu              sync.RWMutex
	epoch           string
	sequence        uint64
	clients         map[string]*client
	providers       map[string]protocol.Provider
	subscribers     map[uint64]chan protocol.Event
	nextSub         uint64
	limits          Limits
	dropped         atomic.Uint64
	reconciliations uint64
}

type client struct {
	ID          string
	State       protocol.PresenceState
	Connections map[string]protocol.Connection
	FirstSeenAt time.Time
	LastSeenAt  time.Time
}

func New(l Limits) (*Engine, error) {
	if l.MaxClients < 1 || l.MaxSubscribers < 1 || l.QueueSize < 1 {
		return nil, fmt.Errorf("engine limits must be positive")
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("create stream epoch: %w", err)
	}
	return &Engine{
		epoch:       hex.EncodeToString(raw[:]),
		clients:     make(map[string]*client),
		providers:   make(map[string]protocol.Provider),
		subscribers: make(map[uint64]chan protocol.Event),
		limits:      l,
	}, nil
}

func connectionID(provider, source string) string {
	return provider + ":" + source
}

func (e *Engine) Associate(provider, source, clientID string, at time.Time, reason string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.clients[clientID]
	if !ok {
		if len(e.clients) >= e.limits.MaxClients && !e.evictOldestAbsentLocked(nil) {
			return fmt.Errorf("client limit reached")
		}
		c = &client{ID: clientID, Connections: make(map[string]protocol.Connection), FirstSeenAt: at}
		e.clients[clientID] = c
	}
	wasPresent := c.State == protocol.StatePresent
	id := connectionID(provider, source)
	conn, existed := c.Connections[id]
	if !existed {
		conn = protocol.Connection{
			ID: id, Provider: provider, SourceInstance: source, ConnectedAt: at,
		}
	}
	conn.LastSeenAt = at
	conn.Stale = false
	c.Connections[id] = conn
	c.State = protocol.StatePresent
	c.LastSeenAt = at
	if !wasPresent {
		e.emitLocked("client.presence_changed", reason, e.publicClientLocked(c))
	} else if !existed {
		e.emitLocked("client.updated", reason, e.publicClientLocked(c))
	}
	return nil
}

func (e *Engine) Disassociate(provider, source, clientID string, at time.Time, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.clients[clientID]
	if !ok {
		return
	}
	id := connectionID(provider, source)
	_, existed := c.Connections[id]
	if !existed {
		return
	}
	delete(c.Connections, id)
	c.LastSeenAt = at
	if len(c.Connections) == 0 {
		c.State = protocol.StateUnknown
		e.emitLocked("client.presence_changed", reason, e.publicClientLocked(c))
	} else {
		e.emitLocked("client.updated", reason, e.publicClientLocked(c))
	}
}

// Reconcile replaces every connection owned by provider with one authoritative
// snapshot, atomically across all source instances.
func (e *Engine) Reconcile(provider string, stations map[string][]string, at time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	wanted := make(map[string]map[string]struct{})
	for source, ids := range stations {
		for _, id := range ids {
			if wanted[id] == nil {
				wanted[id] = make(map[string]struct{})
			}
			wanted[id][source] = struct{}{}
		}
	}
	newClients := 0
	for id := range wanted {
		if _, ok := e.clients[id]; !ok {
			newClients++
		}
	}
	for len(e.clients)+newClients > e.limits.MaxClients {
		if !e.evictOldestAbsentLocked(wanted) {
			return fmt.Errorf("snapshot would exceed client limit")
		}
	}
	e.reconciliations++
	changed := make(map[string]struct{})
	for id, c := range e.clients {
		for connID, conn := range c.Connections {
			if conn.Provider != provider {
				continue
			}
			if _, ok := wanted[id][conn.SourceInstance]; !ok {
				delete(c.Connections, connID)
				changed[id] = struct{}{}
			}
		}
	}
	for id, sources := range wanted {
		c, ok := e.clients[id]
		if !ok {
			c = &client{ID: id, Connections: make(map[string]protocol.Connection), FirstSeenAt: at}
			e.clients[id] = c
		}
		for source := range sources {
			connID := connectionID(provider, source)
			if _, ok := c.Connections[connID]; !ok {
				conn := protocol.Connection{
					ID: connID, Provider: provider, SourceInstance: source,
					ConnectedAt: at, LastSeenAt: at,
				}
				c.Connections[connID] = conn
				changed[id] = struct{}{}
			} else {
				conn := c.Connections[connID]
				conn.LastSeenAt = at
				conn.Stale = false
				c.Connections[connID] = conn
			}
		}
	}
	for _, c := range e.clients {
		old := c.State
		if len(c.Connections) > 0 {
			c.State = protocol.StatePresent
		} else {
			c.State = protocol.StateAbsent
		}
		if c.State != old {
			c.LastSeenAt = at
			e.emitLocked("client.presence_changed", "snapshot_reconciliation", e.publicClientLocked(c))
		} else if _, ok := changed[c.ID]; ok {
			e.emitLocked("client.updated", "snapshot_reconciliation", e.publicClientLocked(c))
		}
	}
	e.emitLocked("state.resynchronized", "authoritative_snapshot", map[string]any{"provider": provider})
	return nil
}

// ReconcileSource replaces the connections owned by one provider source. It is
// the low-cost authoritative recovery path for a source-scoped disconnect.
func (e *Engine) ReconcileSource(
	provider, source string,
	clientIDs []string,
	at time.Time,
) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	wanted := make(map[string]struct{}, len(clientIDs))
	for _, id := range clientIDs {
		wanted[id] = struct{}{}
	}
	newClients := 0
	for id := range wanted {
		if _, ok := e.clients[id]; !ok {
			newClients++
		}
	}
	for len(e.clients)+newClients > e.limits.MaxClients {
		protected := make(map[string]map[string]struct{}, len(wanted))
		for id := range wanted {
			protected[id] = nil
		}
		if !e.evictOldestAbsentLocked(protected) {
			return fmt.Errorf("source snapshot would exceed client limit")
		}
	}

	connection := connectionID(provider, source)
	affected := make(map[string]*client, len(wanted))
	for id, c := range e.clients {
		if _, ok := c.Connections[connection]; ok {
			if _, keep := wanted[id]; !keep {
				delete(c.Connections, connection)
				affected[id] = c
			}
		}
	}
	for id := range wanted {
		c, ok := e.clients[id]
		if !ok {
			c = &client{
				ID: id, Connections: make(map[string]protocol.Connection), FirstSeenAt: at,
			}
			e.clients[id] = c
		}
		if conn, ok := c.Connections[connection]; ok {
			conn.LastSeenAt = at
			conn.Stale = false
			c.Connections[connection] = conn
		} else {
			conn := protocol.Connection{
				ID: connection, Provider: provider, SourceInstance: source,
				ConnectedAt: at, LastSeenAt: at,
			}
			c.Connections[connection] = conn
			affected[id] = c
		}
	}
	for _, c := range affected {
		old := c.State
		if len(c.Connections) > 0 {
			c.State = protocol.StatePresent
		} else {
			c.State = protocol.StateAbsent
		}
		if c.State != old {
			c.LastSeenAt = at
			e.emitLocked(
				"client.presence_changed",
				"source_reconciliation",
				e.publicClientLocked(c),
			)
		} else {
			e.emitLocked(
				"client.updated",
				"source_reconciliation",
				e.publicClientLocked(c),
			)
		}
	}
	e.emitLocked(
		"state.resynchronized",
		"authoritative_source_snapshot",
		map[string]any{"provider": provider, "source_instance": source},
	)
	return nil
}

func (e *Engine) evictOldestAbsentLocked(exclude map[string]map[string]struct{}) bool {
	var candidate *client
	for id, current := range e.clients {
		if current.State != protocol.StateAbsent || len(current.Connections) != 0 {
			continue
		}
		if _, protected := exclude[id]; protected {
			continue
		}
		if candidate == nil || current.LastSeenAt.Before(candidate.LastSeenAt) {
			candidate = current
		}
	}
	if candidate == nil {
		return false
	}
	delete(e.clients, candidate.ID)
	e.emitLocked("client.removed", "retention_limit", e.publicClientLocked(candidate))
	return true
}

func (e *Engine) SetProvider(p protocol.Provider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	old, existed := e.providers[p.ID]
	e.providers[p.ID] = p
	if !existed || old.Status != p.Status || old.LastError != p.LastError {
		e.emitLocked("provider.status", "provider_lifecycle", p)
	}
	if p.Status == "unavailable" {
		for _, c := range e.clients {
			changed := false
			for id, conn := range c.Connections {
				if conn.Provider == p.ID && !conn.Stale {
					conn.Stale = true
					c.Connections[id] = conn
					changed = true
				}
			}
			if changed && c.State != protocol.StateUnknown {
				c.State = protocol.StateUnknown
				e.emitLocked("client.presence_changed", "provider_unavailable", e.publicClientLocked(c))
			}
		}
	}
}

func (e *Engine) Provider(id string) (protocol.Provider, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	p, ok := e.providers[id]
	return p, ok
}

func (e *Engine) Providers() []protocol.Provider {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]protocol.Provider, 0, len(e.providers))
	for _, p := range e.providers {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (e *Engine) Snapshot() protocol.Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.snapshotLocked()
}

func (e *Engine) Client(id string) (protocol.Client, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	c, ok := e.clients[id]
	if !ok {
		return protocol.Client{}, false
	}
	return e.publicClientLocked(c), true
}

// Subscribe atomically captures initial state and registers for subsequent
// events, preventing a snapshot/event race.
func (e *Engine) Subscribe() (protocol.Snapshot, <-chan protocol.Event, func(), error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.subscribers) >= e.limits.MaxSubscribers {
		return protocol.Snapshot{}, nil, nil, fmt.Errorf("stream client limit reached")
	}
	id := e.nextSub
	e.nextSub++
	ch := make(chan protocol.Event, e.limits.QueueSize)
	e.subscribers[id] = ch
	cancel := func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if existing, ok := e.subscribers[id]; ok {
			delete(e.subscribers, id)
			close(existing)
		}
	}
	return e.snapshotLocked(), ch, cancel, nil
}

func (e *Engine) Stats() map[string]any {
	e.mu.RLock()
	defer e.mu.RUnlock()
	connections := 0
	for _, c := range e.clients {
		connections += len(c.Connections)
	}
	return map[string]any{
		"clients": len(e.clients), "connections": connections,
		"stream_clients": len(e.subscribers), "dropped_stream_clients": e.dropped.Load(),
		"sequence": e.sequence, "reconciliations": e.reconciliations,
	}
}

func (e *Engine) snapshotLocked() protocol.Snapshot {
	out := protocol.Snapshot{
		StreamEpoch: e.epoch, Sequence: e.sequence, GeneratedAt: time.Now().UTC(),
		Clients: make([]protocol.Client, 0, len(e.clients)),
	}
	for _, c := range e.clients {
		out.Clients = append(out.Clients, e.publicClientLocked(c))
	}
	sort.Slice(out.Clients, func(i, j int) bool { return out.Clients[i].ID < out.Clients[j].ID })
	return out
}

func (e *Engine) publicClientLocked(c *client) protocol.Client {
	out := protocol.Client{
		ID: c.ID, State: c.State, FirstSeenAt: c.FirstSeenAt, LastSeenAt: c.LastSeenAt,
		Connections: make([]protocol.Connection, 0, len(c.Connections)),
	}
	present := c.State == protocol.StatePresent
	if c.State == protocol.StatePresent || c.State == protocol.StateAbsent {
		out.Present = &present
	}
	for _, conn := range c.Connections {
		out.Connections = append(out.Connections, conn)
	}
	sort.Slice(out.Connections, func(i, j int) bool { return out.Connections[i].ID < out.Connections[j].ID })
	return out
}

func (e *Engine) emitLocked(kind, reason string, data any) {
	e.sequence++
	event := protocol.Event{
		Type: kind, EventID: fmt.Sprintf("%s:%d", e.epoch, e.sequence),
		StreamEpoch: e.epoch, Sequence: e.sequence, Timestamp: time.Now().UTC(),
		Reason: reason, Data: data,
	}
	for id, ch := range e.subscribers {
		select {
		case ch <- event:
		default:
			delete(e.subscribers, id)
			close(ch)
			e.dropped.Add(1)
		}
	}
}
