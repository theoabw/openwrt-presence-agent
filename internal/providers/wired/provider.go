// Package wired detects active LAN clients with direct ARP requests.
package wired

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/identity"
	"github.com/theoabw/openwrt-presence-agent/internal/observation"
)

const (
	providerID          = "wired-arp"
	maxProbeConcurrency = 32
	maxARPOutput        = 64 * 1024
)

var macAddressPattern = regexp.MustCompile(`(?i)(?:[0-9a-f]{2}:){5}[0-9a-f]{2}`)

type Config struct {
	ArpingPath, LeasesFile, Interface string
	Interval, CommandTimeout          time.Duration
	MaxClients                        int
	Excluded                          func(string) bool
}

type Provider struct {
	config Config
	sink   observation.Sink
	logger *slog.Logger
}

func New(c Config, sink observation.Sink, logger *slog.Logger) *Provider {
	return &Provider{config: c, sink: sink, logger: logger}
}

type lease struct{ mac, ip string }

type boundedOutput struct {
	bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedOutput) Write(value []byte) (int, error) {
	size := len(value)
	remaining := b.limit - b.Len()
	if remaining < len(value) {
		b.exceeded = true
		if remaining < 0 {
			remaining = 0
		}
		value = value[:remaining]
	}
	_, _ = b.Buffer.Write(value)
	return size, nil
}

func parseLeases(file *os.File, limit int) ([]lease, error) {
	var leases []lease
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 || net.ParseIP(fields[2]) == nil {
			continue
		}
		id, err := identity.ClientID(fields[1])
		if err != nil {
			continue
		}
		leases = append(leases, lease{mac: id, ip: fields[2]})
		if len(leases) > limit {
			return nil, fmt.Errorf("DHCP lease count exceeds client limit")
		}
	}
	return leases, scanner.Err()
}

func (p *Provider) Run(ctx context.Context) error {
	p.status("initializing", "", time.Time{})
	neighborEvents := make(chan neighborEvent, p.config.MaxClients)
	go p.runNeighborEvents(ctx, neighborEvents)
	var (
		eventsReliable bool
		lastSnapshot   time.Time
	)
	poll := func() {
		at, err := p.snapshot(ctx)
		if err != nil {
			eventsReliable = false
			p.status("unavailable", err.Error(), time.Time{})
			p.logger.Warn("wired presence snapshot failed", "error", err)
			return
		}
		lastSnapshot = at
		eventsReliable = true
		p.status("healthy", "", at)
	}
	poll()
	ticker := time.NewTicker(p.config.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			p.status("stopped", "", time.Time{})
			return nil
		case event := <-neighborEvents:
			if !acceptNeighborEvent(event, eventsReliable, lastSnapshot) {
				continue
			}
			if p.config.Excluded != nil && p.config.Excluded(event.clientID) {
				continue
			}
			if err := p.publishReachable(event.clientID, event.at); err != nil {
				p.logger.Warn("discarding wired neighbor event", "error", err)
			}
		case <-ticker.C:
			poll()
		}
	}
}

func acceptNeighborEvent(event neighborEvent, reliable bool, lastSnapshot time.Time) bool {
	return reliable && event.at.After(lastSnapshot)
}

func (p *Provider) snapshot(parent context.Context) (time.Time, error) {
	file, err := os.Open(p.config.LeasesFile)
	if err != nil {
		return time.Time{}, fmt.Errorf("open DHCP leases: %w", err)
	}
	leases, err := parseLeases(file, p.config.MaxClients)
	_ = file.Close()
	if err != nil {
		return time.Time{}, fmt.Errorf("parse DHCP leases: %w", err)
	}
	if p.config.Excluded != nil {
		filtered := leases[:0]
		for _, candidate := range leases {
			if !p.config.Excluded(candidate.mac) {
				filtered = append(filtered, candidate)
			}
		}
		leases = filtered
	}

	type result struct {
		id        string
		reachable bool
		at        time.Time
	}
	results := make(chan result, len(leases))
	work := make(chan lease)
	var workers sync.WaitGroup
	for range min(maxProbeConcurrency, len(leases)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for candidate := range work {
				reachable := p.reachable(parent, candidate)
				results <- result{
					id: candidate.mac, reachable: reachable, at: time.Now().UTC(),
				}
			}
		}()
	}
	for _, candidate := range leases {
		work <- candidate
	}
	close(work)
	go func() {
		workers.Wait()
		close(results)
	}()

	clients := make([]string, 0, len(leases))
	for result := range results {
		if !result.reachable ||
			(p.config.Excluded != nil && p.config.Excluded(result.id)) {
			continue
		}
		clients = append(clients, result.id)
		if err := p.publishReachable(result.id, result.at); err != nil {
			return time.Time{}, fmt.Errorf("publish reachable client: %w", err)
		}
	}
	sort.Strings(clients)
	at := time.Now().UTC()
	err = p.sink.ApplySnapshot(observation.Snapshot{
		Provider: providerID, ReceivedAt: at,
		Stations: map[string][]string{p.config.Interface: clients},
	})
	return at, err
}

func (p *Provider) publishReachable(clientID string, at time.Time) error {
	return p.sink.Apply(observation.Observation{
		Provider: providerID, SourceInstance: p.config.Interface,
		ReceivedAt: at, ClientID: clientID,
		Kind: observation.WiredReachable, Confidence: observation.Authoritative,
	})
}

func (p *Provider) runNeighborEvents(ctx context.Context, events chan<- neighborEvent) {
	for ctx.Err() == nil {
		err := listenNeighborEvents(ctx, p.config.Interface, events)
		if ctx.Err() != nil {
			return
		}
		p.logger.Warn("wired neighbor event listener stopped; retrying", "error", err)
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (p *Provider) reachable(parent context.Context, candidate lease) bool {
	ctx, cancel := context.WithTimeout(parent, p.config.CommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		p.config.ArpingPath,
		"-c", "1",
		"-w", "1",
		"-I", p.config.Interface,
		candidate.ip,
	)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	output := &boundedOutput{limit: maxARPOutput}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Run(); err != nil || output.exceeded {
		return false
	}
	return arpReplyMatches(output.String(), candidate.mac)
}

func arpReplyMatches(output, clientID string) bool {
	for _, line := range strings.Split(output, "\n") {
		lower := strings.ToLower(line)
		if !strings.Contains(lower, "reply from") &&
			!strings.Contains(lower, "bytes from") {
			continue
		}
		address := macAddressPattern.FindString(line)
		id, err := identity.ClientID(address)
		if err == nil && id == clientID {
			return true
		}
	}
	return false
}

func (p *Provider) status(status, lastError string, snapshot time.Time) {
	p.sink.SetProviderStatus(observation.ProviderStatus{
		ID: providerID, Kind: "ethernet", Status: status,
		SnapshotSupported: true, Sources: []string{p.config.Interface},
		LastSnapshotAt: snapshot, LastError: lastError,
		SnapshotSource: "active-arp:dhcp-leases",
		EventSource:    "netlink:reachable,active-arp:reply",
	})
}
