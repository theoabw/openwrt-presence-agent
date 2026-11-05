package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/api"
	"github.com/theoabw/openwrt-presence-agent/internal/auth"
	"github.com/theoabw/openwrt-presence-agent/internal/config"
	"github.com/theoabw/openwrt-presence-agent/internal/engine"
	"github.com/theoabw/openwrt-presence-agent/internal/identity"
	"github.com/theoabw/openwrt-presence-agent/internal/providers"
)

var version = "development"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 && args[0] == "show-token" {
		return showToken(args[1:])
	}
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-version") {
		fmt.Printf("openwrt-presence-agent %s\n", version)
		return 0
	}
	cfg, err := config.Parse(args)
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		return 2
	}
	logger := newLogger(cfg.LogLevel)
	token, err := config.LoadToken(cfg.TokenFile)
	if err != nil {
		logger.Error("cannot load authentication token", "error", err)
		return 1
	}
	agentID, err := identity.LoadOrCreateAgentID(cfg.AgentIDFile)
	if err != nil {
		logger.Error("cannot load stable agent identity", "error", err)
		return 1
	}
	state, err := engine.New(engine.Limits{
		MaxClients: cfg.MaxClients, MaxSubscribers: cfg.MaxStreamClients,
		QueueSize: cfg.StreamQueueSize, ReconnectGrace: cfg.ReconnectGrace,
	})
	if err != nil {
		logger.Error("cannot initialize state engine", "error", err)
		return 1
	}
	defer state.Stop()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	provider, err := providers.New(cfg, state, logger)
	if err != nil {
		logger.Error("cannot initialize provider", "error", err)
		return 1
	}
	providerResult := make(chan error, 1)
	go func() { providerResult <- provider.Run(ctx) }()

	server := api.New(cfg, state, auth.NewBearer(token), agentID, version, logger)
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	logger.Info("observer started", "version", version, "address", cfg.Address(), "provider", cfg.Provider)
	if ip := net.ParseIP(cfg.ListenAddress); ip != nil && ip.IsLoopback() {
		logger.Info("API listens on loopback only, so only local processes can connect; Home Assistant on another host needs a LAN listen-address (see docs/installation.md)", "address", cfg.Address())
	}
	exitCode := 0
	select {
	case err := <-result:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("API server failed", "error", err)
			exitCode = 1
		}
	case err := <-providerResult:
		if ctx.Err() == nil {
			if err != nil {
				logger.Error("provider stopped unexpectedly", "error", err)
			} else {
				logger.Error("provider stopped unexpectedly")
			}
			exitCode = 1
			stop()
		}
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("API shutdown failed", "error", err)
		return 1
	}
	logger.Info("observer stopped")
	return exitCode
}

func showToken(args []string) int {
	tokenFile := config.Default().TokenFile
	flags := flag.NewFlagSet("show-token", flag.ContinueOnError)
	flags.StringVar(&tokenFile, "token-file", tokenFile, "bearer token file")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "show-token: unexpected arguments: %s\n", strings.Join(flags.Args(), " "))
		return 2
	}
	token, err := config.LoadToken(tokenFile)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			fmt.Fprintf(os.Stderr, "show-token: token file %s does not exist; start the service once to create it\n", tokenFile)
		case errors.Is(err, fs.ErrPermission):
			fmt.Fprintf(os.Stderr, "show-token: cannot read token file %s: permission denied; run as root or the file owner\n", tokenFile)
		default:
			fmt.Fprintf(os.Stderr, "show-token: invalid token file %s: %v\n", tokenFile, err)
		}
		return 1
	}
	fmt.Println(token)
	return 0
}

func newLogger(level string) *slog.Logger {
	var selected slog.Level
	switch level {
	case "debug":
		selected = slog.LevelDebug
	case "warn":
		selected = slog.LevelWarn
	case "error":
		selected = slog.LevelError
	default:
		selected = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: selected}))
}
