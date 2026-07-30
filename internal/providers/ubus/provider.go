package ubus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/identity"
	"github.com/theoabw/openwrt-presence-agent/internal/observation"
)

const (
	providerID          = "ubus-hostapd"
	maxSourceInstances  = 128
	snapshotConcurrency = 2
)

// Config contains only the settings needed by the ubus/hostapd provider.
type Config struct {
	UbusPath          string
	HostapdSocket     string
	ReconcileInterval time.Duration
	DiscoveryInterval time.Duration
	CommandTimeout    time.Duration
	MaxCommandOutput  int64
	MaxEventBytes     int
	MaxClients        int
	QueueSize         int
}

type Provider struct {
	config     Config
	sink       observation.Sink
	logger     *slog.Logger
	mu         sync.Mutex
	status     observation.ProviderStatus
	subscriber eventSubscriber
}

type subscriptionEvent struct {
	generation uint64
	value      observation.Observation
}

type subscriptionResult struct {
	generation uint64
	err        error
}

func New(c Config, sink observation.Sink, logger *slog.Logger) *Provider {
	return newWithSubscriber(c, sink, logger, newGlobalSubscriber(c.HostapdSocket, c.MaxEventBytes))
}

func newWithSubscriber(c Config, sink observation.Sink, logger *slog.Logger, subscriber eventSubscriber) *Provider {
	return &Provider{
		config: c, sink: sink, logger: logger, subscriber: subscriber,
		status: observation.ProviderStatus{
			ID: providerID, Kind: "wifi", Status: "initializing", SnapshotSupported: true,
			SnapshotSource: "ubus:get_clients", EventSource: "hostapd:global-control",
		},
	}
}

func (p *Provider) Run(ctx context.Context) error {
	p.publishStatus()
	var (
		objects      []string
		subCancel    context.CancelFunc
		subResult    <-chan subscriptionResult
		lastSnapshot time.Time
		generation   uint64
	)
	events := make(chan subscriptionEvent, p.config.QueueSize)
	defer func() {
		if subCancel != nil {
			subCancel()
		}
	}()
	discovery := time.NewTicker(p.config.DiscoveryInterval)
	reconcile := time.NewTicker(p.config.ReconcileInterval)
	defer discovery.Stop()
	defer reconcile.Stop()

	refresh := func(forceSnapshot bool) {
		found, err := p.discover(ctx)
		if err != nil {
			p.fail("discovery failed: " + sanitizeError(err))
			return
		}
		changed := !equalStrings(found, objects)
		if len(found) == 0 {
			if subCancel != nil {
				subCancel()
				subCancel = nil
				subResult = nil
				generation++
			}
			p.fail("no hostapd ubus objects discovered")
			return
		}
		if changed {
			objects = found
			if subCancel != nil {
				subCancel()
			}
			subResult = nil
			subCancel = nil
		}
		if subResult == nil {
			generation++
			currentGeneration := generation
			var subCtx context.Context
			subCtx, subCancel = context.WithCancel(ctx)
			result := make(chan subscriptionResult, 1)
			subResult = result
			go func(current []string) {
				result <- subscriptionResult{
					generation: currentGeneration,
					err:        p.subscriber.Subscribe(subCtx, current, currentGeneration, events),
				}
			}(append([]string(nil), objects...))
		}
		if forceSnapshot || changed || lastSnapshot.IsZero() {
			if err := p.snapshot(ctx, objects); err != nil {
				p.fail("snapshot failed: " + sanitizeError(err))
				return
			}
			lastSnapshot = time.Now().UTC()
		}
		p.healthy(objects, lastSnapshot)
	}

	refresh(true)
	for {
		select {
		case <-ctx.Done():
			p.setStatus("stopped", "", objects, lastSnapshot, time.Time{})
			return nil
		case <-discovery.C:
			refresh(false)
		case <-reconcile.C:
			if len(objects) == 0 {
				refresh(true)
				continue
			}
			if err := p.snapshot(ctx, objects); err != nil {
				p.fail("snapshot failed: " + sanitizeError(err))
				continue
			}
			lastSnapshot = time.Now().UTC()
			p.healthy(objects, lastSnapshot)
		case event := <-events:
			if event.generation != generation {
				continue
			}
			p.noteEvent(event.value.ReceivedAt)
			if event.value.Kind == observation.WiFiDisassociated {
				if err := p.sourceSnapshot(ctx, event.value.SourceInstance); err != nil {
					p.fail("post-disassociation source snapshot failed: " + sanitizeError(err))
					if subCancel != nil {
						subCancel()
					}
					subResult = nil
					subCancel = nil
					refresh(true)
				} else {
					lastSnapshot = time.Now().UTC()
					p.healthy(objects, lastSnapshot)
				}
				continue
			}
			if err := p.sink.Apply(event.value); err != nil {
				p.logger.Warn("discarding provider event", "error", err)
				continue
			}
		case result := <-subResult:
			subResult = nil
			subCancel = nil
			if result.generation != generation {
				continue
			}
			if ctx.Err() == nil && result.err != nil {
				p.fail("subscription failed: " + sanitizeError(result.err))
				refresh(true)
			}
		}
	}
}

func (p *Provider) discover(parent context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(parent, p.config.CommandTimeout)
	defer cancel()
	output, err := run(ctx, p.config.UbusPath, p.config.MaxCommandOutput, "list", "hostapd.*")
	if err != nil {
		return nil, err
	}
	var objects []string
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "hostapd.") && len(line) <= 64 {
			objects = append(objects, line)
		}
	}
	sort.Strings(objects)
	objects = compact(objects)
	if len(objects) > maxSourceInstances {
		return nil, fmt.Errorf("discovered %d hostapd objects, limit is %d", len(objects), maxSourceInstances)
	}
	return objects, nil
}

func (p *Provider) snapshot(parent context.Context, objects []string) error {
	stations := make(map[string][]string, len(objects))
	type result struct {
		object string
		ids    []string
		err    error
	}
	work := make(chan string)
	results := make(chan result, len(objects))
	workers := min(snapshotConcurrency, len(objects))
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for object := range work {
				ids, err := p.snapshotSource(parent, object)
				results <- result{object: object, ids: ids, err: err}
			}
		}()
	}
	go func() {
		for _, object := range objects {
			work <- object
		}
		close(work)
		group.Wait()
		close(results)
	}()
	for result := range results {
		if result.err != nil {
			return result.err
		}
		stations[result.object] = result.ids
	}
	if err := p.sink.ApplySnapshot(observation.Snapshot{
		Provider: providerID, ReceivedAt: time.Now().UTC(), Stations: stations,
	}); err != nil {
		return err
	}
	return nil
}

func (p *Provider) sourceSnapshot(parent context.Context, object string) error {
	ids, err := p.snapshotSource(parent, object)
	if err != nil {
		return err
	}
	return p.sink.ApplySourceSnapshot(observation.SourceSnapshot{
		Provider: providerID, SourceInstance: object,
		ReceivedAt: time.Now().UTC(), Clients: ids,
	})
}

func (p *Provider) snapshotSource(parent context.Context, object string) ([]string, error) {
	ctx, cancel := context.WithTimeout(parent, p.config.CommandTimeout)
	defer cancel()
	output, err := run(
		ctx,
		p.config.UbusPath,
		p.config.MaxCommandOutput,
		"call",
		object,
		"get_clients",
	)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", object, err)
	}
	ids, err := decodeSnapshot(output, p.config.MaxClients)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", object, err)
	}
	return ids, nil
}

func decodeSnapshot(data []byte, maxClients int) ([]string, error) {
	var root struct {
		Clients json.RawMessage `json:"clients"`
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("decode get_clients response: %w", err)
	}
	if len(root.Clients) == 0 || bytes.Equal(root.Clients, []byte("null")) {
		return nil, fmt.Errorf("get_clients response has no clients map")
	}
	var clients map[string]json.RawMessage
	if err := json.Unmarshal(root.Clients, &clients); err != nil {
		return nil, fmt.Errorf("decode clients map: %w", err)
	}
	if len(clients) > maxClients {
		return nil, fmt.Errorf("snapshot has %d clients, limit is %d", len(clients), maxClients)
	}
	out := make([]string, 0, len(clients))
	for address, rawClient := range clients {
		var client struct {
			Assoc      bool `json:"assoc"`
			Authorized bool `json:"authorized"`
		}
		if err := json.Unmarshal(rawClient, &client); err != nil {
			continue
		}
		if !client.Assoc || !client.Authorized {
			continue
		}
		id, err := identity.ClientID(address)
		if err != nil {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func (p *Provider) publishStatus() {
	p.mu.Lock()
	status := p.status
	p.mu.Unlock()
	p.sink.SetProviderStatus(status)
}

func (p *Provider) healthy(objects []string, snapshot time.Time) {
	p.setStatus("healthy", "", objects, snapshot, time.Time{})
}

func (p *Provider) fail(message string) {
	p.logger.Warn("ubus provider degraded", "error", message)
	p.mu.Lock()
	lastEvent := p.status.LastEventAt
	sources := append([]string(nil), p.status.Sources...)
	snapshot := p.status.LastSnapshotAt
	p.mu.Unlock()
	p.setStatus("unavailable", message, sources, snapshot, lastEvent)
}

func (p *Provider) noteEvent(at time.Time) {
	p.mu.Lock()
	p.status.LastEventAt = at
	status := p.status
	p.mu.Unlock()
	p.sink.SetProviderStatus(status)
}

func (p *Provider) setStatus(status, message string, objects []string, snapshot, event time.Time) {
	p.mu.Lock()
	p.status.Status = status
	p.status.LastError = message
	p.status.Sources = append([]string(nil), objects...)
	if !snapshot.IsZero() {
		p.status.LastSnapshotAt = snapshot
	}
	if !event.IsZero() {
		p.status.LastEventAt = event
	}
	current := p.status
	p.mu.Unlock()
	p.sink.SetProviderStatus(current)
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 512 {
		message = message[:512]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' {
			return -1
		}
		return r
	}, message)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func compact(values []string) []string {
	if len(values) == 0 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
