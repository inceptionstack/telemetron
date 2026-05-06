# Design: Multi-Pack Adapter Support (Pi + OpenClaw)

## Goal
Telemetron should detect and monitor both `.openclaw` and `.pi` session directories,
reporting each as a separate pack with its own metrics. Detection should be fully
automatic via a single `telemetron detect` command.

## Current Architecture
- Single `mode` field in config (`openclaw`)
- One collector runs at a time via `collectorapi.New(cfg.Collectors[cfg.Mode], ...)`
- Enrollment sends `pack: cfg.Mode` (single value)
- `internal/openclaw/` is a self-registering collector (via `init()` → `collectorapi.Register`)

## Proposed Design

### 1. `telemetron detect` Command

**The single entry point for all lowkey installers.** Replaces the need for
installers to know about modes, session dirs, or multi-pack config.

```bash
# What lowkey installers call (openclaw, roundhouse, or any future pack):
telemetron detect --endpoint https://.../v1/metrics
```

Behavior:
1. Scans all candidate home dirs for known pack directories
2. For each detected pack that isn't already configured:
   - Writes config file (primary or instance-named)
   - Enrolls with the server (gets token)
   - Installs systemd unit (primary or instance-named)
   - Starts the service
3. Skips packs already running (idempotent)
4. Returns summary of what was detected and configured

```
$ telemetron detect --endpoint https://.../v1/metrics

Scanning home directories...
  ✓ openclaw  /home/ec2-user/.openclaw/agents/main/sessions/
  ✓ pi        /home/ec2-user/.pi/agent/sessions/

Setting up openclaw...
  ✓ Config written: /etc/telemetron/config.yaml
  ✓ Enrolled (install_id: abc-123)
  ✓ telemetron.service installed and started

Setting up pi...
  ✓ Config written: /etc/telemetron/config-pi.yaml
  ✓ Enrolled (install_id: def-456)
  ✓ telemetron-pi.service installed and started

Done: 2 packs configured
```

CLI flags:
```
telemetron detect [flags]

Flags:
  --endpoint <url>         Metrics endpoint (required)
  --enroll-endpoint <url>  Enrollment endpoint (default: derived from endpoint)
  --dry-run                Show what would be detected without changes
  --force                  Re-configure even if already set up
  --mode <name>            Only detect/configure this specific mode
```

### 2. Multi-Instance Architecture

Multi-pack = **multiple independent telemetron processes** sharing one binary.

```
┌──────────────────────────────────────┐
│            Same EC2 Host             │
├──────────────────────────────────────┤
│                                      │
│  telemetron.service (primary)        │
│  ├─ config: /etc/telemetron/config.yaml
│  ├─ mode: openclaw                   │
│  ├─ token: /etc/telemetron/token     │
│  └─ scans: ~/.openclaw/.../sessions/ │
│                                      │
│  telemetron-pi.service (instance)    │
│  ├─ config: /etc/telemetron/config-pi.yaml
│  ├─ mode: pi                         │
│  ├─ token: /etc/telemetron/token-pi  │
│  └─ scans: ~/.pi/agent/sessions/    │
│                                      │
│  /var/lib/telemetron/bin/telemetron  │
│  (ONE binary, shared, auto-updates) │
└──────────────────────────────────────┘
```

Each instance has:
- Its own config file
- Its own token (separate enrollment)
- Its own systemd unit
- Its own state file, status file
- Its own `install_id`

Shared across instances:
- Binary (one auto-update updates all)
- Machine ID
- Tier detection
- `telemetron detect` re-scans all on each run

### 3. Instance Naming Convention & Path Resolution

```
Primary (first detected, or openclaw):
  config:  /etc/telemetron/config.yaml
  token:   /etc/telemetron/token
  unit:    telemetron.service
  state:   /var/lib/telemetron/state.json
  status:  /var/lib/telemetron/status.json
  install-id: /etc/telemetron/install-id
  update-state: /var/lib/telemetron/update-state.json  (primary only)

Secondary (named by mode):
  config:  /etc/telemetron/config-<mode>.yaml
  token:   /etc/telemetron/token-<mode>
  unit:    telemetron-<mode>.service
  state:   /var/lib/telemetron/state-<mode>.json
  status:  /var/lib/telemetron/status-<mode>.json
  install-id: /etc/telemetron/install-id-<mode>
  update-state: N/A (auto-update disabled)
```

The primary slot is backward-compatible with existing single-mode installs.

**Path resolution (addressing singleton-path issue):**

Currently `config.DefaultPaths()` and `internal/updater/paths.go` hardcode
singleton paths. To support instances:

1. Add `--instance <name>` flag to the binary (empty = primary).
2. `config.DefaultPaths(instance string)` suffixes all paths when instance != "":
   ```go
   func DefaultPaths(instance string) Paths {
       suffix := ""
       if instance != "" {
           suffix = "-" + instance
       }
       return Paths{
           Config:    "/etc/telemetron/config" + suffix + ".yaml",
           Token:     "/etc/telemetron/token" + suffix,
           InstallID: "/etc/telemetron/install-id" + suffix,
           State:     "/var/lib/telemetron/state" + suffix + ".json",
           Status:    "/var/lib/telemetron/status" + suffix + ".json",
       }
   }
   ```
3. Systemd unit for secondary passes `--instance pi`:
   ```ini
   ExecStart=/var/lib/telemetron/bin/telemetron start --instance pi
   ```
4. Primary (no `--instance`) uses unsuffixed paths = zero migration needed.
5. `detect` command writes the instance name into each secondary's config
   as `instance: pi` for self-identification on restart.

This keeps all existing single-mode installs working unchanged while cleanly
isolating state for secondary instances.

### 4. New `internal/pi/` Package (Pi Collector)
Mirror of `internal/openclaw/` adapted for Pi's session format:
- `internal/pi/collector.go` — registers as mode `"pi"`
- `internal/pi/derive.go` — Pi-specific tool class map, session type derivation
- `internal/pi/state.go` — file offset tracking (reuse shared state type)
- Session dir: `~/.pi/agent/sessions/*.jsonl`
- Same heartbeat/flush pattern as openclaw

### 5. Detection Logic (`internal/agentdetect/`)

Add `DetectPi()` alongside existing `DetectOpenClaw()`:

```go
func DetectPi(opts Options) (Detection, error) {
    username, err := resolveUser(opts.User)
    // ...
    sessionsDir := filepath.Join(home, ".pi", "agent", "sessions")
    if info, err := os.Stat(sessionsDir); err == nil && info.IsDir() {
        return Detection{
            Mode:       "pi",
            SessionDir: sessionsDir,
            RunAsUser:  username,
            AgentName:  "default",
        }, nil
    }
    return Detection{}, nil
}
```

`detect` command calls all detectors:
```go
func detectAll(opts agentdetect.Options) []agentdetect.Detection {
    var found []agentdetect.Detection
    if d, _ := agentdetect.DetectOpenClaw(opts); d.Mode != "" {
        found = append(found, d)
    }
    if d, _ := agentdetect.DetectPi(opts); d.Mode != "" {
        found = append(found, d)
    }
    return found
}
```

### 6. Enrollment
Each detected mode enrolls independently:
- **Separate install_id per mode** (generated at enrollment time, stored in
  `/etc/telemetron/install-id` for primary, `/etc/telemetron/install-id-<mode>` for secondary)
- Each enrollment sends its own `pack` field
- Server sees them as distinct deployments in DDB/Grafana
- No changes to enrollment protocol or Lambda
- Machine ID (`machine_id`) is shared across instances (same host), but
  `install_id` is unique per mode — allows correlation in Grafana

### 7. Auto-Update Ownership

**Problem:** The auto-updater currently runs in every `start` invocation and
uses a singleton state file (`/var/lib/telemetron/update-state.json`). On a
dual-instance host, both processes would race to download, replace the binary,
and trigger exit-code 64 restarts — corrupting state or double-downloading.

**Solution: Primary-only update ownership.**

- Only the **primary** instance (`telemetron.service`) runs the auto-updater.
- Secondary instances have `auto_update.enabled: false` written in their config
  by `telemetron detect` at setup time.
- Secondaries restart via `PartOf=telemetron.service` in their systemd unit:
  ```ini
  [Unit]
  PartOf=telemetron.service
  After=telemetron.service
  ```
  When the primary restarts (exit 64 after update), systemd cascades the
  restart to all `PartOf` dependents automatically.
- The update state file (`/var/lib/telemetron/update-state.json`) remains
  a singleton — only the primary reads/writes it.
- `telemetron update` CLI also only acts on the primary binary; secondaries
  pick up the new binary on their cascaded restart.

**Guard in code:** `cmd/telemetron/start.go` skips `startAutoUpdater()` when
`cfg.AutoUpdate.Enabled == false` (already the case, just needs config set).

### 8. Lowkey Installer Integration

All pack installers (openclaw, roundhouse, future) use the same sidecar call:

```bash
# In run_optional_sidecar() — identical for ALL packs:
curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | \
  TELEMETRON_ENDPOINT="https://..." \
  TELEMETRON_ENROLL_ENDPOINT="https://..." \
  sudo -E bash -s -- detect
```

No `TELEMETRON_MODE` needed. The `detect` command figures out what's on the
machine and configures accordingly. If the same machine later installs a
second pack, re-running `telemetron detect` picks it up.

### 9. Pi Session Format
Need to understand what Pi/Roundhouse writes. Key questions:
- Where are session files? (`~/.pi/agent/sessions/`)
- What's the JSONL schema? (likely: turns with tool calls, similar to openclaw)
- What metrics to derive? (same: session_start, agent_turn, tool_call, error)

### 10. Version Detection
Add `internal/pi/version.go`:
- Read Pi version from npm package metadata or settings
- Fallback: `pi --version` / `roundhouse --version` CLI

## Implementation Plan (ordered)

1. **Phase 1**: `telemetron detect` command + `DetectPi()`
   - New `cmd/telemetron/detect.go` — the detect subcommand
   - `internal/agentdetect/pi.go` — DetectPi function
   - Instance-aware config/token/unit naming
   - Works for single-mode (backward compat) and multi-mode

2. **Phase 2**: `internal/pi/` collector package
   - Copy openclaw structure, adapt for Pi session format
   - Register as mode `"pi"`
   - Investigate Pi JSONL schema

3. **Phase 3**: Lowkey integration + testing
   - Replace `TELEMETRON_MODE=openclaw` with `detect` in all installers
   - E2E test: dual-mode detection on same host
   - Dashboard: verify both show as separate deployments

## File Changes Summary
```
cmd/telemetron/detect.go            — new (detect subcommand)
cmd/telemetron/main.go              — register detect command, add --instance flag
internal/agentdetect/pi.go          — new (DetectPi function)
internal/config/paths.go            — new (DefaultPaths(instance) replaces hardcoded paths)
internal/pi/collector.go            — new (Pi collector)
internal/pi/derive.go               — new (Pi metric derivation)
internal/pi/state.go                — new (or reuse shared state type)
internal/pi/version.go              — new (Pi version detection)
internal/service/service_linux.go   — instance-named unit support + PartOf
internal/updater/paths.go           — respect instance (primary-only guard)
```

## Key Architectural Decisions
1. **Multi-pack = multiple processes, not goroutines** — preserves single-mode
   invariant in config/start/status/enrollment with zero refactoring.
2. **`telemetron detect` is the universal entry point** — installers don't need
   to know about modes. One command handles detection, setup, enrollment, and
   service installation for all packs found on the machine.
3. **Idempotent** — running `detect` again skips already-configured modes,
   picks up newly installed packs.
4. **Binary shared, auto-update is single** — one update, all instances benefit.
