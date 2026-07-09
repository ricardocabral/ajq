package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// PIDFilePath returns the location of the daemon PID file within the cache
// directory. It is used to track a warm daemon across separate `ajq`
// invocations so that `ajq daemon stop` can terminate a daemon spawned by a
// previous process.
func (c Config) PIDFilePath() string {
	cacheDir := c.CacheDir
	if strings.TrimSpace(cacheDir) == "" {
		cacheDir = defaultCacheDir()
	}
	return filepath.Join(cacheDir, "daemon.pid")
}

// ReaperPIDFilePath returns the location of the idle-reaper PID file within the
// cache directory. It tracks the detached reaper subprocess so a later spawn can
// avoid launching a duplicate reaper.
func (c Config) ReaperPIDFilePath() string {
	cacheDir := c.CacheDir
	if strings.TrimSpace(cacheDir) == "" {
		cacheDir = defaultCacheDir()
	}
	return filepath.Join(cacheDir, "reaper.pid")
}

// APIKeyFilePath returns the location of the managed daemon bearer-token file.
// The key lives beside the PID file so separate `ajq` invocations can reuse a
// warm daemon without exposing the secret in status output.
func (c Config) APIKeyFilePath() string {
	cacheDir := c.CacheDir
	if strings.TrimSpace(cacheDir) == "" {
		cacheDir = defaultCacheDir()
	}
	return filepath.Join(cacheDir, "daemon.key")
}

// writeReaperPIDFile persists pid to the reaper PID file, creating parent
// directories as needed.
func (m *Manager) writeReaperPIDFile(pid int) error {
	path := m.Config.ReaperPIDFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeStateFile0600(path, []byte(strconv.Itoa(pid)+"\n"))
}

// readReaperPIDFile reads the tracked reaper PID. The bool result reports
// whether a valid PID was found.
func (m *Manager) readReaperPIDFile() (int, bool) {
	path := m.Config.ReaperPIDFilePath()
	data, err := os.ReadFile(path) //nolint:gosec // path is the daemon reaper PID file under the configured cache directory.
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// removeReaperPIDFile deletes the reaper PID file, ignoring a missing file.
func (m *Manager) removeReaperPIDFile() {
	_ = os.Remove(m.Config.ReaperPIDFilePath())
}

// writePIDFile persists pid to the configured PID file, creating parent
// directories as needed.
func (m *Manager) writePIDFile(pid int) error {
	path := m.Config.PIDFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeStateFile0600(path, []byte(strconv.Itoa(pid)+"\n"))
}

// readPIDFile reads the tracked PID. The bool result reports whether a valid
// PID was found.
func (m *Manager) readPIDFile() (int, bool) {
	path := m.Config.PIDFilePath()
	data, err := os.ReadFile(path) //nolint:gosec // path is the daemon PID file under the configured cache directory.
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// removePIDFile deletes the PID file and its companion API-key file, ignoring
// missing files.
func (m *Manager) removePIDFile() {
	_ = os.Remove(m.Config.PIDFilePath())
	m.removeAPIKeyFile()
}

func (m *Manager) warmManagedAPIKey() (string, bool) {
	pid, ok := m.readPIDFile()
	if !ok || !m.procAlive(pid) {
		return "", false
	}
	key, err := m.readAPIKeyFile()
	if err != nil {
		return "", false
	}
	return key, true
}

func (m *Manager) createAPIKeyFile() (string, error) {
	key, err := generateAPIKey()
	if err != nil {
		return "", err
	}
	if err := m.writeAPIKeyFile(key); err != nil {
		return "", err
	}
	return key, nil
}

func generateAPIKey() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (m *Manager) writeAPIKeyFile(key string) error {
	path := m.Config.APIKeyFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeStateFile0600(path, []byte(key+"\n"))
}

func writeStateFile0600(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // path is a daemon state file under the configured cache directory.
	if err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = f.Close()
		return err
	}
	_, writeErr := f.Write(data)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(path, 0o600)
}

func (m *Manager) readAPIKeyFile() (string, error) {
	path := m.Config.APIKeyFilePath()
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		return "", fmt.Errorf("daemon API key file has permissions %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is the daemon API key file under the configured cache directory.
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if len(key) != 64 {
		return "", fmt.Errorf("daemon API key has invalid length")
	}
	if _, err := hex.DecodeString(key); err != nil {
		return "", fmt.Errorf("daemon API key is not hex encoded: %w", err)
	}
	return key, nil
}

func (m *Manager) removeAPIKeyFile() {
	_ = os.Remove(m.Config.APIKeyFilePath())
}

// defaultSignalProc delivers sig to the OS process identified by pid.
func defaultSignalProc(pid int, sig os.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Signal(sig)
}

// defaultProcAlive reports whether a process with pid exists, using signal 0.
func defaultProcAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 performs error checking without actually sending a signal.
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}
