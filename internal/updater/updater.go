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
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/inceptionstack/telemetron/internal/fsatomic"
)

const (
	// ExitCodeUpdate is the exit code used to signal systemd to restart
	// after a binary update.
	ExitCodeUpdate = 64

	defaultIntervalMinutes         = 720 // 12 hours (production/external)
	defaultIntervalMinutesFrequent = 30  // 30 minutes (test/internal)
	initialJitterMax       = 5 * time.Minute
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
	if env := os.Getenv("TELEMETRON_AUTO_UPDATE"); env != "" {
		low := strings.ToLower(env)
		return low != "false" && low != "0" && low != "no"
	}
	if c.Enabled != nil {
		return *c.Enabled
	}
	return true
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

// IntervalForTier returns the check interval, using a tier-appropriate
// default when no explicit interval is configured.
// Test and internal tiers check every 30m; external/production every 12h.
func (c Config) IntervalForTier(tier string) time.Duration {
	if env := os.Getenv("TELEMETRON_AUTO_UPDATE_INTERVAL"); env != "" {
		if n, err := strconv.Atoi(env); err == nil && n > 0 {
			return time.Duration(n) * time.Minute
		}
	}
	if c.IntervalMinutes > 0 {
		return time.Duration(c.IntervalMinutes) * time.Minute
	}
	return DefaultIntervalForTier(tier)
}

// DefaultIntervalForTier returns the default update check interval for a tier.
func DefaultIntervalForTier(tier string) time.Duration {
	switch tier {
	case "test", "internal":
		return time.Duration(defaultIntervalMinutesFrequent) * time.Minute
	default:
		return time.Duration(defaultIntervalMinutes) * time.Minute
	}
}

// FlushCounter provides an interface to observe flush counts.
type FlushCounter interface {
	FlushCount() uint64
}

// noopFlushCounter is used when no FlushCounter is provided (manual updates).
type noopFlushCounter struct{}

func (noopFlushCounter) FlushCount() uint64 { return 0 }

// Updater handles auto-update logic.
type Updater struct {
	currentVersion string
	binaryPath     string
	baseURL        string // GitHub API base URL, empty for default
	logger         *slog.Logger
	client         *http.Client
	flushCounter   FlushCounter
	sf             *StateFile
}

// New creates a new Updater.
func New(currentVersion, binaryPath string, logger *slog.Logger, fc FlushCounter) *Updater {
	return &Updater{
		currentVersion: currentVersion,
		binaryPath:     binaryPath,
		logger:         logger,
		client:         &http.Client{Timeout: 60 * time.Second},
		flushCounter:   fc,
		sf:             NewStateFile(DefaultStatePath, logger),
	}
}

// NewForManualUpdate creates an Updater for the `telemetron update` command.
// It has no FlushCounter — manual updates skip the confirmation state machine
// and rely on the caller to restart the service explicitly.
func NewForManualUpdate(currentVersion, binaryPath string) *Updater {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	return &Updater{
		currentVersion: currentVersion,
		binaryPath:     binaryPath,
		logger:         logger,
		client:         &http.Client{Timeout: 60 * time.Second},
		flushCounter:   noopFlushCounter{},
		sf:             NewStateFile(DefaultStatePath, logger),
	}
}

// ApplyRelease downloads, verifies, and installs a release. This is the
// entry point for manual `telemetron update`. Unlike the auto-updater,
// it does not set update_pending state (the caller restarts the service
// explicitly).
func (u *Updater) ApplyRelease(ctx context.Context, rel *Release) error {
	return u.applyCore(ctx, rel, nil)
}

// applyCore is the shared stage → backup → rename pipeline.
// If beforeRename is non-nil it runs after backup succeeds but before
// the binary is swapped — this is the hook point for persisting
// update_pending state in the auto-updater path.
func (u *Updater) applyCore(ctx context.Context, rel *Release, beforeRename func() error) error {
	stagedBinary, cleanup, err := u.stage(ctx, rel)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := u.backupCurrent(); err != nil {
		return err
	}

	if beforeRename != nil {
		if err := beforeRename(); err != nil {
			return err
		}
	}

	if err := os.Rename(stagedBinary, u.binaryPath); err != nil {
		return fmt.Errorf("rename staged binary: %w", err)
	}

	u.logger.Info("binary replaced",
		slog.String("event", "update_applied"),
		slog.String("from", u.currentVersion),
		slog.String("to", rel.TagName),
		slog.String("prev", u.binaryPath+".prev"))

	// Best-effort: sync the other binary path so CLI and service stay aligned.
	u.syncCompanionBinary()

	return nil
}

// syncCompanionBinary copies the updated binary to the other path
// (managed → legacy or legacy → managed) so both stay in sync.
func (u *Updater) syncCompanionBinary() {
	var companion string
	switch u.binaryPath {
	case ManagedBinaryPath:
		companion = LegacyBinaryPath
	case LegacyBinaryPath:
		companion = ManagedBinaryPath
	default:
		return // non-standard path, skip
	}

	data, err := os.ReadFile(u.binaryPath)
	if err != nil {
		return
	}
	// Ensure parent dir exists (managed path may not exist on legacy installs)
	_ = os.MkdirAll(filepath.Dir(companion), 0o755)
	if err := fsatomic.WriteFile(companion, data, fsatomic.WithMode(0o755)); err != nil {
		u.logger.Warn("failed to sync companion binary",
			slog.String("path", companion),
			slog.String("error", err.Error()))
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

// ---------- Rollback (runs early in startup, before full config) ----------

// CheckRollback reads the state file and rolls back if an update crashed.
// Returns true if a rollback was performed (caller should exit).
func CheckRollback(logger *slog.Logger) bool {
	return checkRollback(logger, DefaultStatePath, ManagedBinaryPath)
}

func checkRollback(logger *slog.Logger, statePath, binaryPath string) bool {
	sf := NewStateFile(statePath, logger)
	sf.Load()
	state := sf.Get()

	if !state.UpdatePending {
		return false
	}
	if !state.UpdateStarted {
		// First boot after update — mark started
		if err := sf.Update(func(s *State) { s.UpdateStarted = true }); err != nil {
			// Can't persist started flag — clear pending to avoid infinite
			// first-boot loop. If ClearPending also fails (disk truly broken),
			// the process continues without rollback protection.
			logger.Warn("failed to mark update_started, clearing pending",
				slog.String("event", "update_started_write_failed"),
				slog.String("error", err.Error()))
			sf.ClearPending("")
			return false
		}
		logger.Info("update first boot, marking started",
			slog.String("event", "update_first_boot"),
			slog.String("version", state.PendingVersion))
		return false
	}

	// Crash restart after update — rollback
	logger.Warn("update crash detected, rolling back",
		slog.String("event", "update_rollback"),
		slog.String("failed_version", state.PendingVersion))

	prevPath := binaryPath + ".prev"
	if !fileExists(prevPath) {
		logger.Warn("no .prev binary for rollback, skipping",
			slog.String("event", "update_rollback_skip"))
		sf.ClearPending(state.PendingVersion)
		return false
	}

	if err := os.Rename(prevPath, binaryPath); err != nil {
		logger.Error("rollback rename failed",
			slog.String("event", "update_rollback_failed"),
			slog.String("error", err.Error()))
		sf.ClearPending(state.PendingVersion)
		return false
	}

	sf.ClearPending(state.PendingVersion)
	logger.Info("rollback complete, exiting for restart with restored binary",
		slog.String("event", "update_rollback_done"),
		slog.String("rolled_back_version", state.PendingVersion))
	return true
}

// ---------- Main update loop ----------

// Run starts the update check loop. Blocks until ctx is cancelled.
// Returns ExitCodeUpdate if an update was applied.
func (u *Updater) Run(ctx context.Context, cfg Config, tier string) int {
	interval := cfg.IntervalForTier(tier)
	u.sf.Load()

	delay := u.initialDelay(interval)
	u.logger.Info("auto-update enabled",
		slog.String("event", "updater_start"),
		slog.String("current", u.currentVersion),
		slog.String("interval", interval.String()),
		slog.String("first_check_in", delay.Round(time.Second).String()))

	// If update_pending + started, begin confirmation in background
	state := u.sf.Get()
	if state.UpdatePending && state.UpdateStarted {
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
			u.sf.UpdateBestEffort(func(s *State) {
				s.LastCheck = time.Now().UTC()
			})
			timer.Reset(interval)
		}
	}
}

func (u *Updater) initialDelay(interval time.Duration) time.Duration {
	lastCheck := u.sf.Get().LastCheck
	if !lastCheck.IsZero() {
		elapsed := time.Since(lastCheck)
		remaining := interval - elapsed
		if remaining > 0 {
			if remaining < shortJitterMax {
				remaining += time.Duration(rand.Int63n(int64(shortJitterMax)))
			}
			return remaining
		}
	}
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

	// Skip versions that previously caused a rollback
	state := u.sf.Get()
	if state.RolledBackVersion == strings.TrimPrefix(rel.TagName, "v") ||
		state.RolledBackVersion == rel.TagName {
		u.logger.Warn("skipping previously rolled-back version",
			slog.String("event", "update_skip_rollback"),
			slog.String("version", rel.TagName))
		return 0
	}

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

// ---------- Download, verify, apply ----------

func (u *Updater) downloadAndApply(ctx context.Context, rel *Release) error {
	persistPending := func() error {
		return u.sf.Update(func(s *State) {
			s.UpdatePending = true
			s.UpdateStarted = false
			s.PendingVersion = rel.TagName
			s.LastUpdate = time.Now().UTC()
			s.PreviousVersion = u.currentVersion
			s.CurrentVersion = strings.TrimPrefix(rel.TagName, "v")
		})
	}

	if err := u.applyCore(ctx, rel, persistPending); err != nil {
		// Clear pending state if it was written but rename failed
		state := u.sf.Get()
		if state.UpdatePending {
			u.sf.UpdateBestEffort(func(s *State) {
				s.UpdatePending = false
				s.UpdateStarted = false
				s.PendingVersion = ""
			})
		}
		return err
	}

	return nil
}

// stage downloads, verifies, and extracts the release binary into a staging
// directory. Returns the path to the staged binary and a cleanup function.
func (u *Updater) stage(ctx context.Context, rel *Release) (binaryPath string, cleanup func(), err error) {
	assetName := AssetName(rel.TagName)
	archive, checksums := FindAsset(rel, assetName)
	if archive == nil {
		return "", nil, fmt.Errorf("asset %s not found in release %s", assetName, rel.TagName)
	}
	if checksums == nil {
		return "", nil, fmt.Errorf("checksums.txt not found in release %s", rel.TagName)
	}

	stagingDir := filepath.Join(filepath.Dir(u.binaryPath), "..", "staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create staging dir: %w", err)
	}

	archivePath := filepath.Join(stagingDir, assetName)
	checksumsPath := filepath.Join(stagingDir, "checksums.txt")
	stagedBinary := filepath.Join(stagingDir, "telemetron")

	cleanupFn := func() {
		_ = os.Remove(archivePath)
		_ = os.Remove(checksumsPath)
		_ = os.Remove(stagedBinary) // may already be gone after rename
	}

	if err := u.download(ctx, archive.BrowserDownloadURL, archivePath); err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("download archive: %w", err)
	}
	if err := u.download(ctx, checksums.BrowserDownloadURL, checksumsPath); err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("download checksums: %w", err)
	}
	if err := verifyChecksum(archivePath, checksumsPath, assetName); err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("checksum verification: %w", err)
	}

	u.logger.Info("update downloaded and verified",
		slog.String("event", "update_download"),
		slog.String("version", rel.TagName),
		slog.Bool("checksum_ok", true))

	if err := extractBinary(archivePath, stagedBinary); err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("extract: %w", err)
	}
	if err := os.Chmod(stagedBinary, 0o755); err != nil {
		cleanupFn()
		return "", nil, fmt.Errorf("chmod staged binary: %w", err)
	}

	return stagedBinary, cleanupFn, nil
}

// backupCurrent saves the current binary to .prev (rotating .prev → .prev.bak).
func (u *Updater) backupCurrent() error {
	prevPath := u.binaryPath + ".prev"

	// Rotate existing .prev to .prev.bak
	if fileExists(prevPath) {
		_ = os.Rename(prevPath, u.binaryPath+".prev.bak")
	}

	currentData, err := os.ReadFile(u.binaryPath)
	if err != nil {
		return fmt.Errorf("read current binary: %w", err)
	}

	if err := fsatomic.WriteFile(prevPath, currentData, fsatomic.WithMode(0o755)); err != nil {
		return fmt.Errorf("backup current binary: %w", err)
	}

	return nil
}

// ---------- HTTP download ----------

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
		_ = f.Close()
		_ = os.Remove(dest)
		return err
	}
	if n == maxDownloadBytes {
		_ = f.Close()
		_ = os.Remove(dest)
		return fmt.Errorf("download truncated at %d bytes limit", maxDownloadBytes)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dest)
		return err
	}
	return nil
}

// ---------- Archive verification & extraction ----------

func verifyChecksum(archivePath, checksumsPath, assetName string) error {
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

	data, err := os.ReadFile(checksumsPath)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
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
	defer func() { _ = gz.Close() }()

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
				_ = out.Close()
				_ = os.Remove(dest)
				return err
			}
			if n == maxExtractBytes {
				_ = out.Close()
				_ = os.Remove(dest)
				return fmt.Errorf("extracted binary truncated at %d bytes limit", maxExtractBytes)
			}
			return out.Close()
		}
	}
	return fmt.Errorf("telemetron binary not found in archive")
}

// ---------- Update confirmation ----------

// ConfirmIfPending starts the confirmation goroutine if an update is
// pending. This should be called even when auto-update checks are
// disabled, so that a pending update gets confirmed and the flag
// cleared — preventing false rollback on next restart.
func (u *Updater) ConfirmIfPending(ctx context.Context) {
	u.sf.Load()
	state := u.sf.Get()
	if state.UpdatePending && state.UpdateStarted {
		go u.confirmUpdate(ctx)
	}
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
				if err := u.sf.Update(func(s *State) {
					s.UpdatePending = false
					s.UpdateStarted = false
					s.PendingVersion = ""
					s.RolledBackVersion = ""
				}); err != nil {
					u.logger.Warn("failed to persist update confirmation, will retry",
						slog.String("error", err.Error()))
					continue
				}
				u.logger.Info("update confirmed after successful flushes",
					slog.String("event", "update_confirmed"),
					slog.String("version", u.currentVersion),
					slog.Uint64("flush_count", current))
				return
			}
		}
	}
}
