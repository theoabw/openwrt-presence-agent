package identity

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func LoadOrCreateAgentID(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		value := strings.TrimSpace(string(data))
		decoded, decodeErr := hex.DecodeString(value)
		if decodeErr != nil || len(decoded) != 16 {
			return "", fmt.Errorf("agent identity file is invalid")
		}
		return value, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("read agent identity: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create identity directory: %w", err)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generate agent identity: %w", err)
	}
	value := hex.EncodeToString(raw[:])
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return LoadOrCreateAgentID(path)
		}
		return "", fmt.Errorf("create agent identity: %w", err)
	}
	if _, err := file.WriteString(value + "\n"); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("write agent identity: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close agent identity: %w", err)
	}
	return value, nil
}
