package wired

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theoabw/openwrt-presence-agent/internal/engine"
	"github.com/theoabw/openwrt-presence-agent/pkg/protocol"
)

func TestParseLeases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases")
	data := strings.Join([]string{
		"1 AA:BB:CC:DD:EE:FF 192.168.1.10 host *",
		"malformed",
		"1 invalid 192.168.1.11 host *",
		"1 00:11:22:33:44:55 not-an-ip host *",
	}, "\n")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := parseLeases(file, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].mac != "mac:aa:bb:cc:dd:ee:ff" || got[0].ip != "192.168.1.10" {
		t.Fatalf("parseLeases() = %#v", got)
	}
}

func TestSnapshotRequiresFreshARPReply(t *testing.T) {
	dir := t.TempDir()
	leases := filepath.Join(dir, "leases")
	probe := filepath.Join(dir, "arping")
	if err := os.WriteFile(leases, []byte(
		"1 00:00:00:00:00:01 192.168.1.10 awake *\n"+
			"1 00:00:00:00:00:02 192.168.1.11 asleep *\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(probe, []byte(
		"#!/bin/sh\n[ \"$7\" = \"192.168.1.10\" ]\n",
	), 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := engine.New(engine.Limits{MaxClients: 10, MaxSubscribers: 1, QueueSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	provider := New(Config{
		ArpingPath: probe, LeasesFile: leases, Interface: "br-lan",
		Interval: time.Second, CommandTimeout: time.Second, MaxClients: 10,
	}, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := provider.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	awake, ok := state.Client("mac:00:00:00:00:00:01")
	if !ok || awake.State != protocol.StatePresent {
		t.Fatalf("ARP-responsive client = %#v, found=%v", awake, ok)
	}
	if _, ok := state.Client("mac:00:00:00:00:00:02"); ok {
		t.Fatal("non-responsive lease was treated as an online client")
	}

	// The same retained lease must become absent once it stops replying.
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	awake, ok = state.Client("mac:00:00:00:00:00:01")
	if !ok || awake.State != protocol.StateAbsent {
		t.Fatalf("stopped client = %#v, found=%v", awake, ok)
	}
}

func TestReachableClientIsPublishedBeforeSlowSweepCompletes(t *testing.T) {
	dir := t.TempDir()
	leases := filepath.Join(dir, "leases")
	probe := filepath.Join(dir, "arping")
	if err := os.WriteFile(leases, []byte(
		"1 00:00:00:00:00:01 192.168.1.10 awake *\n"+
			"1 00:00:00:00:00:02 192.168.1.11 asleep *\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(probe, []byte(
		"#!/bin/sh\n"+
			"if [ \"$7\" = \"192.168.1.10\" ]; then exit 0; fi\n"+
			"sleep 1\nexit 1\n",
	), 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := engine.New(engine.Limits{MaxClients: 10, MaxSubscribers: 1, QueueSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	provider := New(Config{
		ArpingPath: probe, LeasesFile: leases, Interface: "br-lan",
		Interval: time.Second, CommandTimeout: 2 * time.Second, MaxClients: 10,
	}, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	done := make(chan error, 1)
	go func() {
		_, err := provider.snapshot(context.Background())
		done <- err
	}()

	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		client, ok := state.Client("mac:00:00:00:00:00:01")
		if ok && client.State == protocol.StatePresent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reachable client waited for the complete sweep")
		}
		time.Sleep(5 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("slow sweep unexpectedly completed early: %v", err)
	default:
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotDoesNotProbeExcludedWiFiClient(t *testing.T) {
	dir := t.TempDir()
	leases := filepath.Join(dir, "leases")
	probe := filepath.Join(dir, "arping")
	if err := os.WriteFile(leases, []byte(
		"1 00:00:00:00:00:01 192.168.1.10 wifi *\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(probe, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := engine.New(engine.Limits{MaxClients: 10, MaxSubscribers: 1, QueueSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	provider := New(Config{
		ArpingPath: probe, LeasesFile: leases, Interface: "br-lan",
		Interval: time.Second, CommandTimeout: time.Second, MaxClients: 10,
		Excluded: func(id string) bool {
			return id == "mac:00:00:00:00:00:01"
		},
	}, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := provider.snapshot(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Client("mac:00:00:00:00:00:01"); ok {
		t.Fatal("excluded Wi-Fi client was published by wired provider")
	}
}

func TestParseLeasesBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "leases")
	if err := os.WriteFile(path, []byte(
		"1 00:00:00:00:00:01 192.168.1.1 a *\n"+
			"1 00:00:00:00:00:02 192.168.1.2 b *\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := parseLeases(file, 1); err == nil {
		t.Fatal("parseLeases accepted excess clients")
	}
}
