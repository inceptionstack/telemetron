# Telemetron Auto-Update Plan

## Goal
Telemetron checks every 12 hours for a new GitHub release, downloads the new binary, replaces itself, and restarts the systemd service. All steps are logged.

## Design

### Approach: In-process update goroutine (systemd-only)
The `start` command spawns a background updater goroutine **only when** the binary path matches the managed install location (`/var/lib/telemetron/bin/telemetron`). This is checked via `os.Executable()` at startup. Non-systemd runs (foreground, macOS, dev) skip the updater entirely. The `TELEMETRON_AUTO_UPDATE=false` env var also disables it.

### Systemd constraints
The current unit has:
- `ProtectSystem=strict` — filesystem read-only except `ReadWritePaths`
- `ReadWritePaths=/var/lib/telemetron` — only writable path
- `PrivateTmp=true` — isolated /tmp per invocation
- `NoNewPrivileges=true` — can't escalate

**Consequence:** The running process cannot write to `/usr/local/bin/` or `/tmp` (visible to other processes). All staging must happen under `/var/lib/telemetron/`.

### Binary relocation
Move the binary from `/usr/local/bin/telemetron` to `/var/lib/telemetron/bin/telemetron`. Update the systemd unit's `ExecStart` to point there. Keep a symlink at `/usr/local/bin/telemetron` for CLI use.

This lets the running process (ec2-user) atomically replace its own binary under `ReadWritePaths`.

### Update flow
```
every 12h (first check after 0-30min jitter, or resume from state file):
  1. GET https://api.github.com/repos/inceptionstack/telemetron/releases/latest
     → parse tag_name (e.g. "v0.3.7")
  2. Semver compare with compiled-in `version` variable (using golang.org/x/mod/semver)
     → if not newer, log "up to date" and sleep
     → if version="dev" or contains "-snapshot", skip check entirely
  3. Find matching asset: telemetron_{version}_{os}_{arch}.tar.gz
  4. Download asset + checksums.txt to /var/lib/telemetron/staging/
  5. Verify SHA256 from checksums.txt
  6. Extract binary to /var/lib/telemetron/staging/telemetron
  7. Replace binary atomically:
     - Rename existing .prev to .prev.bak if it exists (preserves oldest known-good)
     - Copy current binary to /var/lib/telemetron/bin/telemetron.prev
     - Write `update_pending: true` + `update_started: false` + `pending_version` to state file (atomic write, AFTER .prev copy succeeds)
     - os.Rename(staging/telemetron, bin/telemetron) — atomic on same filesystem
     - Note: if crash between .prev copy and rename, update_pending is set but old binary still in place; rollback restores .prev which is identical to current — harmless
  8. Write `last_check` timestamp to state file
  9. Log: "updated from v0.3.6 to v0.3.7, exiting for restart"
  10. Exit with code 64 (custom "update restart" code)
      systemd restarts the process, which now runs the new binary

If step 2 finds no update available, write `last_check` to state and sleep until next interval.
```

### Restart strategy
Keep `Restart=on-failure`. Add `RestartForceExitStatus=64` so exit code 64 is treated as a failure (triggers restart) while normal `os.Exit(0)` from `systemctl stop` / SIGTERM remains a clean stop.

Add `StartLimitIntervalSec=600` and `StartLimitBurst=10` to prevent permanent stop from crash loops. Use `RestartForceExitStatus=64` so exit code 64 triggers a restart under `Restart=on-failure`. Exit 64 counts against the burst limit, but 10 restarts in 10 minutes is generous enough for legitimate update cycles. Do NOT add `SuccessExitStatus=64` — the interaction with `RestartForceExitStatus` is version-dependent and confusing.

If rollback itself fails (old binary also crashes), the service will exhaust `StartLimitBurst` and stay down. This is the correct behavior — a fundamentally broken install needs manual intervention. The last log lines will show the rollback attempt for diagnosis.

### Update confirmation mechanism
Add an in-memory-only `FlushCount uint64` field to `status.Store` (not persisted to JSON, not in `Snapshot`). The OTLP sink increments it via `store.IncrFlush()` (mutex-protected, not atomic) on **every** flush call, including empty flushes with 0 points — an empty flush still proves the binary is functional. On startup, if `update_pending: true`, the updater records the current `FlushCount`. After the count increases by 3, it writes `update_pending: false` to state. The updater reads the count on a timer tick (same interval as flush, 15s). Count resets to 0 on process restart, which is correct — we need 3 flushes after *this* startup.

### Rollback safety
The rollback check runs very early in startup, before full config init — it reads `update-state.json` directly with `os.ReadFile` + `json.Unmarshal` into a minimal struct (`update_pending bool`, `update_started bool`, `rolled_back_version string`). Path is hardcoded to `/var/lib/telemetron/update-state.json` (deliberate constraint — all systemd installs use the default path). This ensures even a fundamentally broken new binary can still roll back, as long as the Go runtime starts. If the JSON is unparseable, skip rollback (don't make things worse).

After rollback, `.prev` is consumed but the restored binary is the previously-confirmed-working version. A subsequent crash would be an unrelated issue, not an update problem — manual intervention is appropriate at that point (`.prev.bak` available). If `.prev` doesn't exist (e.g. dev/test context), rollback is a no-op — log and continue.

All state file writes use atomic write-to-temp-then-rename to prevent corruption from mid-write crashes.

### Version string handling
GitHub `tag_name` includes the `v` prefix (e.g. `v0.3.7`). GoReleaser asset names use version WITHOUT the prefix (e.g. `telemetron_0.3.7_linux_arm64.tar.gz`). Strip the `v` prefix when constructing the download URL. Use `golang.org/x/mod/semver` for comparison (expects `v` prefix).

### Auto-rollback on bad release
Flag-based with explicit state transitions:
1. Before `os.Rename`, write `update_pending: true` + `pending_version` + `update_started: false` to state file
2. `os.Rename(staging → bin)` — atomic
3. Exit with code 64 for restart
4. New binary starts, sees `update_pending: true` + `update_started: false`
5. Set `update_started: true` in state file — this distinguishes "first boot after update" from "crash restart"
6. After 3 successful flush cycles, write `update_pending: false` → update confirmed
7. If new binary crashes and restarts: sees `update_pending: true` + `update_started: true` → this is a crash restart, trigger rollback
8. Rollback: restore from `telemetron.prev` via `os.Rename`, clear `update_pending`, write `rolled_back_version`. Future update checks skip that version.

This handles: crash during startup, crash after partial init, slow-onset bugs (within first 3 flushes ≈ 45s). Bugs that manifest later are not auto-rolled-back (accepted — same as any deploy).

### File layout
```
internal/updater/
  updater.go       — UpdateLoop(), Check(), Download(), Apply()
  updater_test.go  — unit tests with httptest server
  github.go        — GitHub release API types, FetchLatest()
  version.go       — semver comparison helpers
cmd/telemetron/
  start.go         — launch updater goroutine
  update.go        — `telemetron update` CLI command (manual check+apply)
```

### Config
Add `auto_update` section to the existing `/etc/telemetron/config.yaml`:
```yaml
auto_update:
  enabled: true           # default: true
  interval_minutes: 720   # default: 720 (12h)
```

The `auto_update` key is a top-level config field (alongside `mode`, `endpoint`, etc.), parsed by viper like the rest of the config.

Environment overrides:
- `TELEMETRON_AUTO_UPDATE=false` disables auto-update
- `TELEMETRON_AUTO_UPDATE_INTERVAL=360` sets interval in minutes

Config struct:
```go
type AutoUpdate struct {
    Enabled         *bool `mapstructure:"enabled" yaml:"enabled"`           // nil = default true
    IntervalMinutes int   `mapstructure:"interval_minutes" yaml:"interval_minutes"` // 0 = default 720
}
```

Note: `Enabled` is `*bool` so we can distinguish "not set" (default true) from "explicitly false".

### State file
`/var/lib/telemetron/update-state.json`:
```json
{
  "last_check": "2026-05-04T19:00:00Z",
  "last_update": "2026-05-04T19:00:30Z",
  "current_version": "0.3.7",
  "previous_version": "0.3.6",
  "update_pending": false,
  "update_started": false,
  "pending_version": "",
  "rolled_back_version": ""
}
```

On startup, read this file. If `last_check` is within the interval, skip to remaining sleep time (prevents rapid check loops on restart). Add jitter (0-5min) when remaining sleep time is < 5min to avoid thundering-herd checks after fleet restarts.

### Logging
All update activity logged as structured JSON via slog:
```json
{"level":"INFO","msg":"update check","event":"update_check","current":"0.3.6","latest":"0.3.7","update_available":true}
{"level":"INFO","msg":"update downloaded","event":"update_download","version":"0.3.7","bytes":18880488,"checksum_ok":true}
{"level":"INFO","msg":"binary replaced","event":"update_applied","from":"0.3.6","to":"0.3.7","prev":"/var/lib/telemetron/bin/telemetron.prev"}
{"level":"INFO","msg":"exiting for update restart","event":"update_restart","exit_code":64}
```

### Security
- SHA256 checksum verification from release checksums.txt — this is a **transport integrity** check (guards against download corruption/truncation), NOT an authenticity check. A compromised GitHub release can replace both asset and checksum.
- **Accepted risk:** no signature verification. Mitigation: public repo with branch protection, GoReleaser provenance. Future: add cosign verification.
- HTTPS only for all downloads
- No GitHub auth needed (public repo)
- Rate: 1 check request per 12h per instance (+ 2 download requests only when updating). 18 instances = 1.5 checks/hr steady state, well under 60/hr unauthenticated limit
- Restart storms mitigated by state file check before jitter; fleet-wide restarts won't all hit GitHub simultaneously
- Asset name uses `runtime.GOOS` + `runtime.GOARCH` (matches GoReleaser output exactly)

### Edge cases
- **No internet**: log warning, retry next interval
- **Partial download**: write to staging dir, only rename on full success + checksum match
- **version="dev" or "-snapshot"**: skip update check entirely
- **Rapid restart loop**: state file tracks last_check; won't re-check within interval
- **Rollback**: auto-rollback if new binary crashes before 3 successful flushes; restores from `.prev` copy then clears flag and records `rolled_back_version` to prevent re-update loop; manual fallback: `cp telemetron.prev telemetron && systemctl restart telemetron`
- **Disk full**: download fails, logged, retry next interval

### Install changes
- `telemetron install` creates `/var/lib/telemetron/bin/` and moves binary there
- Symlink: `/usr/local/bin/telemetron` → `/var/lib/telemetron/bin/telemetron`
- Unit file: `ExecStart=/var/lib/telemetron/bin/telemetron start --config ...`
- Unit file: add `RestartForceExitStatus=64`, `StartLimitIntervalSec=600`, `StartLimitBurst=10`
- Existing installs: migration in `telemetron install --upgrade` or next `setup`

## Implementation order
1. `internal/updater/github.go` — fetch latest release, parse version
2. `internal/updater/version.go` — semver comparison, skip logic for dev/snapshot
3. `internal/updater/updater.go` — download, verify checksum, stage, apply, state file
4. `internal/updater/updater_test.go` — httptest mock server tests
5. Config additions (`auto_update` section)
6. Wire into `start.go` — background goroutine with jitter
7. `cmd/telemetron/update.go` — manual `telemetron update` command
8. Update `install.go` — binary relocation, symlink, unit changes
9. Docs update (README, configuration.md)
