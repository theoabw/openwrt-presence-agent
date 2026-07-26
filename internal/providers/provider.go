// Package providers selects platform-specific observation providers.
package providers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/theoabw/openwrt-presence-agent/internal/config"
	"github.com/theoabw/openwrt-presence-agent/internal/observation"
	"github.com/theoabw/openwrt-presence-agent/internal/providers/ubus"
	"github.com/theoabw/openwrt-presence-agent/internal/providers/wired"
)

// Provider publishes normalized observations until its context is canceled.
type Provider interface {
	Run(context.Context) error
}

// New constructs the configured observation provider.
func New(c config.Config, sink observation.Sink, logger *slog.Logger) (Provider, error) {
	switch c.Provider {
	case "ubus":
		wifi := ubus.New(ubus.Config{
			UbusPath:          c.UbusPath,
			HostapdSocket:     c.HostapdSocket,
			ReconcileInterval: c.ReconcileInterval,
			DiscoveryInterval: c.DiscoveryInterval,
			CommandTimeout:    c.CommandTimeout,
			MaxCommandOutput:  c.MaxCommandOutput,
			MaxEventBytes:     c.MaxEventBytes,
			MaxClients:        c.MaxClients,
			QueueSize:         c.ProviderQueueSize,
		}, sink, logger)
		ethernet := wired.New(wired.Config{
			ArpingPath: c.ArpingPath, LeasesFile: c.DHCPLeasesFile,
			Interface: c.LANInterface, Interval: c.WiredReconcileInterval,
			CommandTimeout: c.CommandTimeout, MaxClients: c.MaxClients,
		}, sink, logger)
		return group{wifi, ethernet}, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", c.Provider)
	}
}

type group []Provider

func (g group) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(g))
	for _, provider := range g {
		go func(p Provider) { results <- p.Run(ctx) }(provider)
	}
	for range g {
		if err := <-results; err != nil {
			cancel()
			return err
		}
	}
	return nil
}
