package providers

import (
	"sync"

	"github.com/theoabw/openwrt-presence-agent/internal/observation"
)

// wifiAssociations mirrors authoritative hostapd connections while forwarding
// observations to the state engine. The wired provider uses it to avoid probing
// clients that already have a lower-latency Wi-Fi presence source.
type wifiAssociations struct {
	sink        observation.Sink
	mu          sync.RWMutex
	connections map[string]map[string]struct{}
	ready       chan struct{}
	readyOnce   sync.Once
}

func newWiFiAssociations(sink observation.Sink) *wifiAssociations {
	return &wifiAssociations{
		sink: sink, connections: make(map[string]map[string]struct{}),
		ready: make(chan struct{}),
	}
}

func (w *wifiAssociations) Ready() <-chan struct{} {
	return w.ready
}

func (w *wifiAssociations) markReady() {
	w.readyOnce.Do(func() { close(w.ready) })
}

func (w *wifiAssociations) Contains(clientID string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.connections[clientID]) != 0
}

func (w *wifiAssociations) Apply(value observation.Observation) error {
	if err := w.sink.Apply(value); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	switch value.Kind {
	case observation.WiFiAssociated:
		if w.connections[value.ClientID] == nil {
			w.connections[value.ClientID] = make(map[string]struct{})
		}
		w.connections[value.ClientID][value.SourceInstance] = struct{}{}
	case observation.WiFiDisassociated:
		delete(w.connections[value.ClientID], value.SourceInstance)
		if len(w.connections[value.ClientID]) == 0 {
			delete(w.connections, value.ClientID)
		}
	}
	return nil
}

func (w *wifiAssociations) ApplySnapshot(value observation.Snapshot) error {
	if err := w.sink.ApplySnapshot(value); err != nil {
		return err
	}
	connections := make(map[string]map[string]struct{})
	for source, clients := range value.Stations {
		for _, clientID := range clients {
			if connections[clientID] == nil {
				connections[clientID] = make(map[string]struct{})
			}
			connections[clientID][source] = struct{}{}
		}
	}
	w.mu.Lock()
	w.connections = connections
	w.mu.Unlock()
	w.markReady()
	return nil
}

func (w *wifiAssociations) ApplySourceSnapshot(value observation.SourceSnapshot) error {
	if err := w.sink.ApplySourceSnapshot(value); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for clientID, sources := range w.connections {
		delete(sources, value.SourceInstance)
		if len(sources) == 0 {
			delete(w.connections, clientID)
		}
	}
	for _, clientID := range value.Clients {
		if w.connections[clientID] == nil {
			w.connections[clientID] = make(map[string]struct{})
		}
		w.connections[clientID][value.SourceInstance] = struct{}{}
	}
	return nil
}

func (w *wifiAssociations) SetProviderStatus(value observation.ProviderStatus) {
	w.sink.SetProviderStatus(value)
	if value.Status == "unavailable" {
		// Do not block Ethernet indefinitely on routers without usable radios.
		w.markReady()
	}
}
