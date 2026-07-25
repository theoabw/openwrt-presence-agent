package ubus

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

var sanitizedEnvironment = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}

type limitWriter struct {
	buffer bytes.Buffer
	limit  int64
	total  int64
}

func (w *limitWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	if w.total > w.limit {
		remaining := w.limit - int64(w.buffer.Len())
		if remaining > 0 {
			_, _ = w.buffer.Write(p[:remaining])
		}
		return len(p), errOutputLimit
	}
	return w.buffer.Write(p)
}

var errOutputLimit = fmt.Errorf("command output exceeded configured limit")

func run(ctx context.Context, executable string, maxOutput int64, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Env = sanitizedEnvironment
	stdout := &limitWriter{limit: maxOutput}
	stderr := &limitWriter{limit: 16 * 1024}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if stdout.total > maxOutput {
		return nil, errOutputLimit
	}
	if err != nil {
		message := bytes.TrimSpace(stderr.buffer.Bytes())
		if len(message) == 0 {
			return nil, fmt.Errorf("%s failed: %w", executable, err)
		}
		return nil, fmt.Errorf("%s failed: %w: %s", executable, err, message)
	}
	return io.ReadAll(&stdout.buffer)
}
