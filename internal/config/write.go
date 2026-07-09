package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// WriteOptions makes config-file mutation testable without changing process
// globals. Zero values use the same environment/home resolution as Load.
type WriteOptions struct {
	Getenv  func(string) string
	HomeDir func() (string, error)
}

// SetModel persists the top-level model setting in ajq's TOML config. It
// preserves unrelated TOML keys through a comments-free round-trip; comments
// and original formatting are intentionally not preserved.
func SetModel(model string, opts WriteOptions) (string, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	path, _, err := configPath(getenv, opts.HomeDir)
	if err != nil {
		return "", err
	}

	raw := map[string]any{}
	data, err := os.ReadFile(path) //nolint:gosec // path is the resolved ajq config path from explicit env or user home config.
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read config %s: %w", path, err)
		}
	} else {
		if _, err := toml.Decode(string(data), &raw); err != nil {
			return "", fmt.Errorf("parse config %s: %w", path, err)
		}
		if err := rejectCredentialKeyPaths(nil, raw); err != nil {
			return "", err
		}
	}
	raw["model"] = model

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(raw); err != nil {
		return "", fmt.Errorf("encode config %s: %w", path, err)
	}
	if err := atomicWrite(path, buf.Bytes(), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func atomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("create temp config %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp config %s: %w", tmpName, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("chmod temp config %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp config %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename config %s: %w", path, err)
	}
	return nil
}
