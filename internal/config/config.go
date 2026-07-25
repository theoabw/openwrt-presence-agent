package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddress      string
	Port               int
	TokenFile          string
	AgentIDFile        string
	UbusPath           string
	HostapdSocket      string
	Provider           string
	ReconcileInterval  time.Duration
	DiscoveryInterval  time.Duration
	CommandTimeout     time.Duration
	MaxCommandOutput   int64
	MaxEventBytes      int
	MaxClients         int
	MaxHTTPConnections int
	ProviderQueueSize  int
	MaxStreamClients   int
	StreamQueueSize    int
	LogLevel           string
}

func Default() Config {
	return Config{
		ListenAddress: "127.0.0.1", Port: 8787,
		TokenFile:         "/etc/openwrt-presence-agent/token",
		AgentIDFile:       "/etc/openwrt-presence-agent/agent-id",
		UbusPath:          "/bin/ubus",
		HostapdSocket:     "/var/run/hostapd/global",
		Provider:          "ubus",
		ReconcileInterval: 30 * time.Second, DiscoveryInterval: 10 * time.Second,
		CommandTimeout: 5 * time.Second, MaxCommandOutput: 1024 * 1024,
		MaxEventBytes: 64 * 1024, MaxClients: 512,
		MaxHTTPConnections: 16, ProviderQueueSize: 256,
		MaxStreamClients: 4, StreamQueueSize: 64, LogLevel: "info",
	}
}

func Parse(args []string) (Config, error) {
	c := Default()
	fs := flag.NewFlagSet("openwrt-presence-agent", flag.ContinueOnError)
	fs.StringVar(&c.ListenAddress, "listen-address", c.ListenAddress, "IP address to listen on")
	fs.IntVar(&c.Port, "port", c.Port, "TCP port")
	fs.StringVar(&c.TokenFile, "token-file", c.TokenFile, "bearer token file")
	fs.StringVar(&c.AgentIDFile, "agent-id-file", c.AgentIDFile, "stable agent identity file")
	fs.StringVar(&c.UbusPath, "ubus-path", c.UbusPath, "absolute ubus executable path")
	fs.StringVar(&c.HostapdSocket, "hostapd-socket", c.HostapdSocket, "absolute hostapd global control socket path")
	fs.StringVar(&c.Provider, "provider", c.Provider, "observation provider")
	fs.DurationVar(&c.ReconcileInterval, "reconcile-interval", c.ReconcileInterval, "snapshot reconciliation interval")
	fs.DurationVar(&c.DiscoveryInterval, "discovery-interval", c.DiscoveryInterval, "object discovery interval")
	fs.DurationVar(&c.CommandTimeout, "command-timeout", c.CommandTimeout, "ubus call timeout")
	fs.Int64Var(&c.MaxCommandOutput, "max-command-output", c.MaxCommandOutput, "maximum ubus response bytes")
	fs.IntVar(&c.MaxEventBytes, "max-event-bytes", c.MaxEventBytes, "maximum hostapd event bytes")
	fs.IntVar(&c.MaxClients, "max-clients", c.MaxClients, "maximum retained clients")
	fs.IntVar(&c.MaxHTTPConnections, "max-http-connections", c.MaxHTTPConnections, "maximum accepted HTTP connections")
	fs.IntVar(&c.ProviderQueueSize, "provider-queue-size", c.ProviderQueueSize, "maximum buffered provider events")
	fs.IntVar(&c.MaxStreamClients, "max-stream-clients", c.MaxStreamClients, "maximum event consumers")
	fs.IntVar(&c.StreamQueueSize, "stream-queue-size", c.StreamQueueSize, "events buffered per consumer")
	fs.StringVar(&c.LogLevel, "log-level", c.LogLevel, "error, warn, info, or debug")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if net.ParseIP(c.ListenAddress) == nil {
		return fmt.Errorf("listen-address must be a literal IP address")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if c.TokenFile == "" || c.TokenFile[0] != '/' {
		return fmt.Errorf("token-file must be an absolute path")
	}
	if c.AgentIDFile == "" || c.AgentIDFile[0] != '/' {
		return fmt.Errorf("agent-id-file must be an absolute path")
	}
	if c.UbusPath == "" || c.UbusPath[0] != '/' {
		return fmt.Errorf("ubus-path must be an absolute path")
	}
	if c.HostapdSocket == "" || c.HostapdSocket[0] != '/' {
		return fmt.Errorf("hostapd-socket must be an absolute path")
	}
	if c.Provider != "ubus" {
		return fmt.Errorf("unsupported provider %q", c.Provider)
	}
	if c.ReconcileInterval < time.Second || c.DiscoveryInterval < time.Second || c.CommandTimeout < time.Second {
		return fmt.Errorf("timeouts and intervals must be at least 1s")
	}
	if c.MaxCommandOutput < 4096 || c.MaxCommandOutput > 16*1024*1024 {
		return fmt.Errorf("max-command-output must be between 4096 and 16777216")
	}
	if c.MaxEventBytes < 1024 || c.MaxEventBytes > 1024*1024 {
		return fmt.Errorf("max-event-bytes must be between 1024 and 1048576")
	}
	if c.MaxClients < 1 || c.MaxClients > 100000 {
		return fmt.Errorf("max-clients must be between 1 and 100000")
	}
	if c.MaxHTTPConnections < 1 || c.MaxHTTPConnections > 4096 {
		return fmt.Errorf("max-http-connections must be between 1 and 4096")
	}
	if c.ProviderQueueSize < 1 || c.ProviderQueueSize > 65536 {
		return fmt.Errorf("provider-queue-size must be between 1 and 65536")
	}
	if c.MaxStreamClients < 1 || c.MaxStreamClients > 1024 || c.StreamQueueSize < 1 || c.StreamQueueSize > 4096 {
		return fmt.Errorf("stream limits are outside supported bounds")
	}
	switch c.LogLevel {
	case "error", "warn", "info", "debug":
	default:
		return fmt.Errorf("invalid log-level %q", c.LogLevel)
	}
	return nil
}

func (c Config) Address() string {
	return net.JoinHostPort(c.ListenAddress, strconv.Itoa(c.Port))
}

func LoadToken(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat token file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("token file is not a regular file")
	}
	if info.Mode().Perm()&0o007 != 0 {
		return "", fmt.Errorf("token file permissions %04o allow access by other users", info.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) < 43 || len(token) > 512 {
		return "", fmt.Errorf("token must encode at least 256 bits and be at most 512 bytes")
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return "", errors.New("token contains whitespace")
	}
	return token, nil
}
