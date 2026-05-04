// SPDX-License-Identifier: Apache-2.0

// Package updater implements auto-update for telemetron.
// It checks GitHub releases periodically, downloads new versions,
// and atomically replaces the running binary.
package updater

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/inceptionstack/telemetron/internal/fsatomic"
)

const (
	// ExitCodeUpdate is the exit code used to signal systemd to restart
	// after a binary update.
	ExitCodeUpdate = 64

	// ManagedBinDir is the directory for the managed binary.
	ManagedBinDir = "/var/lib/telemetron/bin"

	// ManagedBinaryPath is the full path to the managed binary.
	ManagedBinaryPath = ManagedBinDir + "/telemetron"

	// DefaultStatePath is the hardcoded path for update state.
	DefaultStatePath = "/var/lib/telemetron/update-state.json"

	defaultIntervalMinutes = 720 // 12 hours
	initialJitterMax       = 30 * time.Minute
	shortJitterMax         = 5 * time.Minute
	confirmFlushes         = 3
	maxDownloadBytes       = 500 << 20 // 500 MB
	maxExtractBytes        = 500 << 20 // 500 MB
)

// Config holds auto-update configuration.
type Config struct {
	Enabled         *bool `mapstructure:"enabled" yaml:"enabled"`
	IntervalMinutes int   `mapstructure:"interval_minutes" yaml:"interval_minutes"`
}

// IsEnabled returns whether auto-update is enabled (default: true).
func (c Config) IsEnabled() bool {
	// Env override takes precedence
	if env := os.Getenv("TELEMETRON_AUTO_UPDATE"); env != "" {
		low := strings.ToLower(env)
		return low != "false" && low != "0" && low != "no"
	}
	if c.Enabled != nil {
		return *c.Enabled
	}
	return true // default
}

// Interval returns the check interval.
func (c Config) Interval() time.Duration {
	if env := os.Getenv("TELEMETRON_AUTO_UPDATE_INTERVAL"); env != "" {
		if n, err := strconv.Atoi(env); err == nil && n > 0 {
			return time.Duration(n) * time.Minute
		}
	}
	if c.IntervalMinutes > 0 {
		return time.Duration(c.IntervalMinutes) * time.Minute
	}
	return time.Duration(defaultIntervalMinutes) * time.Minute
}

// State persists update check state across restarts.
type State struct {
	LastCheck         time.Time `json:"last_check"`
	LastUpdate        time.Time `json:"last_update,omitempty"`
	CurrentVersion    string    `json:"current_version,omitempty"`
	PreviousVersion   string    `json:"previous_version,omitempty"`
	UpdatePending     bool      `json:"update_pending"`
	UpdateStarted     bool      `json:"update_started"`
	PendingVersion    string    `json:"pending_version,omitempty"`
	RolledBackVersion string    `json:"rolled_back_version,omitempty"`
}

// FlushCounter provides an interface to observe flush counts.
type FlushCounter interface {
	FlushCount() uint64
}

// Updater handles auto-update logic.
type Updater struct {
	currentVersion string
	binaryPath     string
	statePath      string
	baseURL        string // GitHub API base URL, empty for default
	logger         *slog.Logger
	client         *http.Client
	flushCounter   FlushCounter

	mu    sync.Mutex
	state State
}

// New creates a new Updater.
func New(currentVersion, binaryPath string, logger *slog.Logger, fc FlushCounter) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		binaryPath:     binaryPath,
		statePath:      DefaultStatePath,
		logger:         logger,
		client:         &http.Client{Timeout: 60 * time.Second},
		flushCounter:   fc,
	}
}

// IsManagedInstall checks if the running binary is in the managed path.
func IsManagedInstall() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return false
	}
	return resolved == ManagedBinaryPath
}

// CheckRollback runs early in startup, before full config init.
// It reads the state file and rolls back if an update crashed.
// Returns true if a rollback was performed (caller should exit).
func CheckRollback(logger *slog.Logger) bool {
	return checkRollback(logger, DefaultStatePath, ManagedBinaryPath)
}

func checkRollback(logger *slog.Logger, statePath, binaryPath string) bool {
	data, err := os.ReadFile(statePath)
	if err != nil {
		return false // no state file, nothing to do
	}
	var state struct {
		UpdatePending bool   `json:"update_pending"`
		UpdateStarted bool   `json:"update_started"`
		RolledBackVer string `json:"rolled_back_version"`
		PendingVer    string `json:"pending_version"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return false // unparseable, don't make things worse
	}
	if !state.UpdatePending {
		return false
	}
	if !state.UpdateStarted {
		// First boot after update — mark started
		var full State
		_ = json.Unmarshal(data, &full)
		full.UpdateStarted = true
		writeStateTo(statePath, full)
		logger.Info("update first boot, marking started",
			slog.String("event", "update_first_boot"),
			slog.String("version", state.PendingVer))
		return false
	}

	// Crash restart after update — rollback
	logger.Warn("update crash detected, rolling back",
		slog.String("event", "update_rollback"),
		slog.String("failed_version", state.PendingVer))

	prevPath := binaryPath + ".prev"
	if _, err := os.Stat(prevPath); err != nil {
		logger.Warn("no .prev binary for rollback, skipping",
			slog.String("event", "update_rollback_skip"))
		clearPending(statePath, state.PendingVer)
		return false
	}

	// Atomic rollback: rename .prev → binary
	if err := os.Rename(prevPath, binaryPath); err != nil {
		logger.Error("rollback rename failed",
			slog.String("event", "update_rollback_failed"),
			slog.String("error", err.Error()))
		clearPending(statePath, state.PendingVer)
		return false
	}

	clearPending(statePath, state.PendingVer)
	logger.Info("rollback complete, exiting for restart with restored binary",
		slog.String("event", "update_rollback_done"),
		slog.String("rolled_back_version", state.PendingVer))
	return true
}

func clearPending(statePath, rolledBackVersion string) {
	data, _ := os.ReadFile(statePath)
	var state State
	_ = json.Unmarshal(data, &state)
	state.UpdatePending = false
	state.UpdateStarted = false
	state.PendingVersion = ""
	state.RolledBackVersion = rolledBackVersion
	writeStateTo(statePath, state)
}


func writeStateTo(path string, state State) {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	_ = fsatomic.WriteFile(path, data)
}

// Run starts the update check loop. Blocks until ctx is cancelled.
// Returns ExitCodeUpdate if an update was applied.
func (u *Updater) Run(ctx context.Context, cfg Config) int {
	interval := cfg.Interval()

	// Load state
	u.loadState()

	// Calculate initial delay
	delay := u.initialDelay(interval)
	u.logger.Info("auto-update enabled",
		slog.String("event", "updater_start"),
		slog.String("current", u.currentVersion),
		slog.String("interval", interval.String()),
		slog.String("first_check_in", delay.Round(time.Second).String()))

	// If update_pending, start confirmation goroutine
	u.mu.Lock()
	pending := u.state.UpdatePending && u.state.UpdateStarted
	u.mu.Unlock()
	if pending {
		go u.confirmUpdate(ctx)
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return 0
		case <-timer.C:
			code := u.check(ctx)
			if code == ExitCodeUpdate {
				return code
			}
			u.mu.Lock()
			u.state.LastCheck = time.Now().UTC()
			writeStateTo(u.statePath, u.state)
			u.mu.Unlock()
			timer.Reset(interval)
		}
	}
}

func (u *Updater) loadState() {
	data, err := os.ReadFile(u.statePath)
	if err != nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	_ = json.Unmarshal(data, &u.state)
}

func (u *Updater) initialDelay(interval time.Duration) time.Duration {
	u.mu.Lock()
	lastCheck := u.state.LastCheck
	u.mu.Unlock()
	if !lastCheck.IsZero() {
		elapsed := time.Since(lastCheck)
		remaining := interval - elapsed
		if remaining > 0 {
			// Add jitter if remaining is short
			if remaining < shortJitterMax {
				remaining += time.Duration(rand.Int63n(int64(shortJitterMax)))
			}
			return remaining
		}
	}
	// No state or stale — random initial jitter
	return time.Duration(rand.Int63n(int64(initialJitterMax)))
}

func (u *Updater) check(ctx context.Context) int {
	if ShouldSkip(u.currentVersion) {
		u.logger.Debug("skipping update check for dev/snapshot build")
		return 0
	}

	rel, err := FetchLatest(ctx, u.client, u.baseURL)
	if err != nil {
		u.logger.Warn("update check failed",
			slog.String("event", "update_check_failed"),
			slog.String("error", err.Error()))
		return 0
	}

	u.logger.Info("update check",
		slog.String("event", "update_check"),
		slog.String("current", u.currentVersion),
		slog.String("latest", rel.TagName),
		slog.Bool("update_available", IsNewerVersion(u.currentVersion, rel.TagName)))

	if !IsNewerVersion(u.currentVersion, rel.TagName) {
		return 0
	}

	// Check if this version was previously rolled back
	u.mu.Lock()
	if u.state.RolledBackVersion == strings.TrimPrefix(rel.TagName, "v") ||
		u.state.RolledBackVersion == rel.TagName {
		u.mu.Unlock()
		u.logger.Warn("skipping previously rolled-back version",
			slog.String("event", "update_skip_rollback"),
			slog.String("version", rel.TagName))
		return 0
	}
	u.mu.Unlock()

	if err := u.downloadAndApply(ctx, rel); err != nil {
		u.logger.Error("update failed",
			slog.String("event", "update_failed"),
			slog.String("version", rel.TagName),
			slog.String("error", err.Error()))
		return 0
	}

	u.logger.Info("exiting for update restart",
		slog.String("event", "update_restart"),
		slog.String("from", u.currentVersion),
		slog.String("to", rel.TagName),
		slog.Int("exit_code", ExitCodeUpdate))
	return ExitCodeUpdate
}

func (u *Updater) downloadAndApply(ctx context.Context, rel *Release) error {
	assetName := AssetName(rel.TagName)
	archive, checksums := FindAsset(rel, assetName)
	if archive == nil {
		return fmt.Errorf("asset %s not found in release %s", assetName, rel.TagName)
	}
	if checksums == nil {
		return fmt.Errorf("checksums.txt not found in release %s", rel.TagName)
	}

	stagingDir := filepath.Join(filepath.Dir(u.binaryPath), "..", "staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	// Download archive
	archivePath := filepath.Join(stagingDir, assetName)
	if err := u.download(ctx, archive.BrowserDownloadURL, archivePath); err != nil {
		return fmt.Errorf("download archive: %w", err)
	}
	defer os.Remove(archivePath)

	// Download checksums
	checksumsPath := filepath.Join(stagingDir, "checksums.txt")
	if err := u.download(ctx, checksums.BrowserDownloadURL, checksumsPath); err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	defer os.Remove(checksumsPath)

	// Verify checksum
	if err := verifyChecksum(archivePath, checksumsPath, assetName); err != nil {
		return fmt.Errorf("checksum verification: %w", err)
	}

	u.logger.Info("update downloaded and verified",
		slog.String("event", "update_download"),
		slog.String("version", rel.TagName),
		slog.Bool("checksum_ok", true))

	// Extract binary
	binaryStaging := filepath.Join(stagingDir, "telemetron")
	if err := extractBinary(archivePath, binaryStaging); err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	if err := os.Chmod(binaryStaging, 0o755); err != nil {
		return fmt.Errorf("chmod staged binary: %w", err)
	}

	// Backup current binary
	prevPath := u.binaryPath + ".prev"
	prevBakPath := u.binaryPath + ".prev.bak"

	// Rename existing .prev to .prev.bak
	if _, err := os.Stat(prevPath); err == nil {
		_ = os.Rename(prevPath, prevBakPath)
	}

	// Atomic copy current → .prev (write to temp, rename)
	currentData, err := os.ReadFile(u.binaryPath)
	if err != nil {
		return fmt.Errorf("read current binary: %w", err)
	}
	if err := fsatomic.WriteFile(prevPath, currentData, fsatomic.WithMode(0o755)); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}

	// Write update_pending BEFORE the rename
	u.mu.Lock()
	u.state.UpdatePending = true
	u.state.UpdateStarted = false
	u.state.PendingVersion = rel.TagName
	u.state.LastUpdate = time.Now().UTC()
	u.state.PreviousVersion = u.currentVersion
	u.state.CurrentVersion = strings.TrimPrefix(rel.TagName, "v")
	writeStateTo(u.statePath, u.state)
	u.mu.Unlock()

	// Atomic rename staged → binary
	if err := os.Rename(binaryStaging, u.binaryPath); err != nil {
		// Rename failed — clear pending state since update wasn't applied
		u.mu.Lock()
		u.state.UpdatePending = false
		u.state.UpdateStarted = false
		u.state.PendingVersion = ""
		writeStateTo(u.statePath, u.state)
		u.mu.Unlock()
		return fmt.Errorf("rename staged binary: %w", err)
	}

	u.logger.Info("binary replaced",
		slog.String("event", "update_applied"),
		slog.String("from", u.currentVersion),
		slog.String("to", rel.TagName),
		slog.String("prev", prevPath))

	return nil
}

func (u *Updater) download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "telemetron-updater")

	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	if resp.ContentLength > maxDownloadBytes {
		return fmt.Errorf("response too large: %d bytes (limit %d)", resp.ContentLength, maxDownloadBytes)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}

	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownloadBytes))
	if err != nil {
		f.Close()
		os.Remove(dest)
		return err
	}
	// Check if response was truncated by LimitReader
	if n == maxDownloadBytes {
		f.Close()
		os.Remove(dest)
		return fmt.Errorf("download truncated at %d bytes limit", maxDownloadBytes)
	}
	if err := f.Close(); err != nil {
		os.Remove(dest)
		return err
	}
	return nil
}

func verifyChecksum(archivePath, checksumsPath, assetName string) error {
	// Compute actual hash
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	actual := hex.EncodeToString(h.Sum(nil))

	// Read expected from checksums.txt
	data, err := os.ReadFile(checksumsPath)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		// Format: <hash>  <filename>
		parts := strings.Fields(line)
		if len(parts) == 2 && parts[1] == assetName {
			if parts[0] == actual {
				return nil
			}
			return fmt.Errorf("checksum mismatch: expected %s, got %s", parts[0], actual)
		}
	}
	return fmt.Errorf("%s not found in checksums.txt", assetName)
}

func extractBinary(archivePath, dest string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) == "telemetron" && hdr.Typeflag == tar.TypeReg {
			out, err := os.Create(dest)
			if err != nil {
				return err
			}
			n, err := io.Copy(out, io.LimitReader(tr, maxExtractBytes))
			if err != nil {
				out.Close()
				os.Remove(dest)
				return err
			}
			if n == maxExtractBytes {
				out.Close()
				os.Remove(dest)
				return fmt.Errorf("extracted binary truncated at %d bytes limit", maxExtractBytes)
			}
			return out.Close()
		}
	}
	return fmt.Errorf("telemetron binary not found in archive")
}

func (u *Updater) confirmUpdate(ctx context.Context) {
	u.confirmUpdateWithInterval(ctx, 15*time.Second)
}

func (u *Updater) confirmUpdateWithInterval(ctx context.Context, tickInterval time.Duration) {
	startCount := u.flushCounter.FlushCount()
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current := u.flushCounter.FlushCount()
			if current-startCount >= confirmFlushes {
				u.mu.Lock()
				u.state.UpdatePending = false
				u.state.UpdateStarted = false
				u.state.PendingVersion = ""
				// Clear rolled_back_version on successful new update
				u.state.RolledBackVersion = ""
				writeStateTo(u.statePath, u.state)
				u.mu.Unlock()
				u.logger.Info("update confirmed after successful flushes",
					slog.String("event", "update_confirmed"),
					slog.String("version", u.currentVersion),
					slog.Uint64("flush_count", current))
				return
			}
		}
	}
}
