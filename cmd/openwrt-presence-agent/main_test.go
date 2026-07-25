package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	previous := version
	version = "1.2.3-test"
	t.Cleanup(func() { version = previous })

	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousStdout := os.Stdout
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = previousStdout })

	if code := run([]string{"--version"}); code != 0 {
		t.Fatalf("run returned %d", code)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(output)); got != "openwrt-presence-agent 1.2.3-test" {
		t.Fatalf("version output = %q", got)
	}
}
