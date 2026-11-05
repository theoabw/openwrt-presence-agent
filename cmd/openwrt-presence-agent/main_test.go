package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testToken = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUV"

func captureRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	stdoutRead, stdoutWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrRead, stderrWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previousStdout, previousStderr := os.Stdout, os.Stderr
	previousLogger := slog.Default()
	os.Stdout, os.Stderr = stdoutWrite, stderrWrite
	slog.SetDefault(slog.New(slog.NewTextHandler(stderrWrite, nil)))
	defer func() {
		os.Stdout, os.Stderr = previousStdout, previousStderr
		slog.SetDefault(previousLogger)
	}()

	code := run(args)
	if err := stdoutWrite.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrWrite.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := io.ReadAll(stdoutRead)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(stderrRead)
	if err != nil {
		t.Fatal(err)
	}
	return code, string(stdout), string(stderr)
}

func writeTokenFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

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

func TestShowTokenPrintsOnlyToken(t *testing.T) {
	path := writeTokenFile(t, testToken+"\n")
	code, stdout, stderr := captureRun(t, "show-token", "--token-file", path)
	if code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr)
	}
	if stdout != testToken+"\n" {
		t.Fatalf("stdout = %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestShowTokenMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	code, stdout, stderr := captureRun(t, "show-token", "--token-file", path)
	if code == 0 {
		t.Fatal("run succeeded without a token file")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.TrimSpace(stderr) == "" {
		t.Fatal("missing error on stderr")
	}
}

func TestShowTokenRejectsInvalidTokenFiles(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"wrong length", "too-short"},
		{"invalid characters", strings.Repeat("a", 24) + " " + strings.Repeat("b", 23)},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := writeTokenFile(t, test.content)
			code, stdout, stderr := captureRun(t, "show-token", "--token-file", path)
			if code == 0 {
				t.Fatal("run accepted an invalid token file")
			}
			if stdout != "" {
				t.Fatalf("stdout = %q", stdout)
			}
			if strings.TrimSpace(stderr) == "" {
				t.Fatal("missing error on stderr")
			}
		})
	}
}

func TestShowTokenUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode 0000 token file")
	}
	path := writeTokenFile(t, testToken)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(path); err == nil {
		t.Skip("token file is readable despite mode 0000")
	}
	code, stdout, stderr := captureRun(t, "show-token", "--token-file", path)
	if code == 0 {
		t.Fatal("run succeeded on an unreadable token file")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.TrimSpace(stderr) == "" {
		t.Fatal("missing error on stderr")
	}
}

func TestUnknownArgumentFails(t *testing.T) {
	code, stdout, stderr := captureRun(t, "not-a-command")
	if code == 0 {
		t.Fatal("run accepted an unknown argument")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "unexpected arguments") {
		t.Fatalf("stderr = %q", stderr)
	}
}
