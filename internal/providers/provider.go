// Package providers selects platform-specific observation providers.
package providers

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/theoabw/openwrt-presence-agent/internal/config"
	"github.com/theoabw/openwrt-presence-agent/internal/observation"
	"github.com/theoabw/openwrt-presence-agent/internal/providers/ubus"
)

// Provider publishes normalized observations until its context is canceled.
type Provider interface {
	Run(context.Context) error
}

// New constructs the configured observation provider.
func New(c config.Config, sink observation.Sink, logger *slog.Logger) (Provider, error) {
	switch c.Provider {
	case "ubus":
		return ubus.New(ubus.Config{
			UbusPath:          c.UbusPath,
			HostapdSocket:     c.HostapdSocket,
			ReconcileInterval: c.ReconcileInterval,
			DiscoveryInterval: c.DiscoveryInterval,
			CommandTimeout:    c.CommandTimeout,
			MaxCommandOutput:  c.MaxCommandOutput,
			MaxEventBytes:     c.MaxEventBytes,
			MaxClients:        c.MaxClients,
			QueueSize:         c.ProviderQueueSize,
		}, sink, logger), nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", c.Provider)
	}
}
