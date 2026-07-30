package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestConservativeDefaults(t *testing.T) {
	c := Default()
	if c.ListenAddress != "127.0.0.1" {
		t.Fatalf("default address = %q", c.ListenAddress)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.MaxClients != 512 || c.MaxHTTPConnections != 16 || c.MaxStreamClients != 4 {
		t.Fatalf("resource defaults are unexpectedly large: %#v", c)
	}
}

func TestUCIDefaultsMatchBinaryDefaults(t *testing.T) {
	file, err := os.Open("../../packaging/openwrt/files/openwrt-presence-agent.config")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	values := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 3 && fields[0] == "option" {
			values[fields[1]] = strings.Trim(fields[2], "'")
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}

	c := Default()
	expected := map[string]string{
		"listen_address":           c.ListenAddress,
		"port":                     strconv.Itoa(c.Port),
		"token_file":               c.TokenFile,
		"agent_id_file":            c.AgentIDFile,
		"ubus_path":                c.UbusPath,
		"hostapd_socket":           c.HostapdSocket,
		"arping_path":              c.ArpingPath,
		"dhcp_leases_file":         c.DHCPLeasesFile,
		"lan_interface":            c.LANInterface,
		"provider":                 c.Provider,
		"reconcile_interval":       c.ReconcileInterval.String(),
		"wired_reconcile_interval": c.WiredReconcileInterval.String(),
		"discovery_interval":       c.DiscoveryInterval.String(),
		"command_timeout":          c.CommandTimeout.String(),
		"max_command_output":       strconv.FormatInt(c.MaxCommandOutput, 10),
		"max_event_bytes":          strconv.Itoa(c.MaxEventBytes),
		"max_clients":              strconv.Itoa(c.MaxClients),
		"max_http_connections":     strconv.Itoa(c.MaxHTTPConnections),
		"provider_queue_size":      strconv.Itoa(c.ProviderQueueSize),
		"max_stream_clients":       strconv.Itoa(c.MaxStreamClients),
		"stream_queue_size":        strconv.Itoa(c.StreamQueueSize),
		"log_level":                c.LogLevel,
	}
	for name, want := range expected {
		if got := values[name]; got != want {
			t.Errorf("UCI %s = %q, binary default = %q", name, got, want)
		}
	}
}

func TestLoadTokenRejectsUnsafePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(path); err == nil {
		t.Fatal("LoadToken accepted world-readable credentials")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadToken(path); err != nil {
		t.Fatal(err)
	}
}

func TestPackagedTokenGenerationNeedsOnlyBusyBoxDefaults(t *testing.T) {
	const generator = "tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 48"
	for _, path := range []string{
		"../../packaging/openwrt/Makefile",
		"../../packaging/openwrt/files/openwrt-presence-agent.init",
		"../../scripts/build-ipk.sh",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, generator) {
			t.Errorf("%s does not use the BusyBox-only token generator", path)
		}
		if strings.Contains(text, "/dev/urandom | base64") {
			t.Errorf("%s still requires the optional base64 applet", path)
		}
	}
}

func TestPackagedArpingDependencyAndPath(t *testing.T) {
	makefile, err := os.ReadFile("../../packaging/openwrt/Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(makefile), "+iputils-arping") {
		t.Error("OpenWrt package does not depend on iputils-arping")
	}
	if strings.Contains(string(makefile), " +arping") {
		t.Error("OpenWrt package still depends on the nonexistent arping package")
	}

	if got := Default().ArpingPath; got != "/usr/bin/arping" {
		t.Errorf("default arping path = %q", got)
	}
}
