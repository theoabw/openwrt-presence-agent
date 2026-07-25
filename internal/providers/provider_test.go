package providers

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/theoabw/openwrt-presence-agent/internal/config"
	"github.com/theoabw/openwrt-presence-agent/internal/engine"
	"github.com/theoabw/openwrt-presence-agent/internal/providers/ubus"
)

func TestNewSelectsUbusProvider(t *testing.T) {
	state, err := engine.New(engine.Limits{MaxClients: 10, MaxSubscribers: 1, QueueSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := New(config.Default(), state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := provider.(*ubus.Provider); !ok {
		t.Fatalf("New() returned %T", provider)
	}
}

func TestNewRejectsUnknownProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Provider = "unknown"
	state, err := engine.New(engine.Limits{MaxClients: 10, MaxSubscribers: 1, QueueSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(cfg, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil || !strings.Contains(err.Error(), `unsupported provider "unknown"`) {
		t.Fatalf("New() error = %v", err)
	}
}
