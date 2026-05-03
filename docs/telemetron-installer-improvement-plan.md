# telemetron installer — E2E findings & v0.2.0 improvement plan

Status: **ready for implementation** (revised 2026-05-03 after Codex review)
Author: Loki@FastStart
Reviewers: Codex (provided technical corrections below)
Test run: clean AL2023 arm64 t4g.small, SSM-only, telemetron v0.1.0 (commit `d920080`)

## Summary

E2E on a fresh box worked end-to-end, but left operators guessing at several cliff edges. Health check false-negatives, ambiguous session-dir resolution under root/SSM, silent partial writes, missing diagnostics, and two unsupported-platform landmines (Darwin, non-systemd Linux) all need to ship in v0.2.0.

v0.2.0 is a **patch release that preserves the current setup contract** (`installed AND health-verified = success`). Anything that weakens the contract is pushed to v0.3.0.

Top seven fixes total ~6-7h and are scoped to not pull cloud-specific coupling into the core CLI.

---

## Findings — revised

Severity: 🔴 bug · 🟠 rough edge · 🟢 nice-to-have (v0.3.0+)

### 🔴 1. Root / non-interactive session-dir resolution is under-specified

**What I originally wrote (wrong):** "setup auto-detects session-dir from the installer's HOME not SUDO_USER — add SUDO_USER fallback."

**What's actually happening** (per Codex code review):
- `internal/agentdetect/agentdetect.go:142` already prefers `$SUDO_USER` for detection
- `internal/service/service_linux.go:171` and `cmd/telemetron/setup.go:320` already prefer `$SUDO_USER` for run-as
- The E2E failure mode was: running as **root** under SSM `AWS-RunShellScript`, where `$SUDO_USER` is **unset** (SSM doesn't use sudo). Detection can't find a candidate session dir, yet `setup` proceeds anyway and writes `/root/.openclaw/...` — contradicting `docs/telemetron-setup-plan.md:261` and `:368` which say ambiguous resolution must fail deterministically.

**Fix (patch-sized):**
- When `$SUDO_USER` is absent AND no `--run-as`/`--session-dir` was supplied AND the auto-detector cannot resolve a unique candidate, `setup` must **fail non-interactively** with a clear hint:
  ```
  Error: cannot resolve session-dir under UID 0 with no $SUDO_USER set.
  Pass --run-as <user> --session-dir <path>, or set TELEMETRON_RUN_AS / TELEMETRON_SESSION_DIR.
  ```
- Walk plausible home dirs (`/home/*`, `/Users/*` on Darwin) for `.openclaw/agents/main/sessions` — if exactly one exists, use it; if zero or multiple, fail with the hint above.
- Also fix: `cmd/telemetron/setup.go:326` `resolveInputs` reloads endpoint/mode/deployment/tier from config but **not** `session-dir` or `run-as`. A rerun can silently redetect different values. Persist + reload all five.

### 🔴 2. Health check hard-fails on cold-box latency (keep contract, fix timeout + diagnostics)

**What I originally wrote:** "Bump to 60s and warn-don't-fail."

**Codex correction:** Warn-only **violates the setup contract** (`docs/setup-contract.md:53` + `docs/telemetron-setup-plan.md:277`) which defines success as "installed AND health-verified." A patch release must not silently downgrade that. The hard-fail in `cmd/telemetron/setup.go:245` is intentional.

**Fix (patch-sized):**
- Bump default timeout from 30s → **60s**
- Add `--health-timeout <duration>` flag and `TELEMETRON_HEALTH_TIMEOUT` env
- Keep **failure-by-default** on timeout
- **Surface the last HTTP response in the error** (see finding #6), so the error becomes actionable instead of opaque

### 🔴 3. `install.sh` fails on `HOME: unbound variable` under minimal envs

**Upgraded from 🟠 — Codex flagged that SSM `AWS-RunShellScript` is a supported path today.** Hard failure at `install.sh:36` blocks the SSM-based install pattern entirely.

**Fix (XS):** Near the top of `install.sh`:
```bash
: "${HOME:=/root}"
export HOME
```
Or drop `set -u` from install.sh. Short script, tradeoff isn't worth it.

### 🔴 4. Unsupported-platform preconditions

**New finding from Codex review.**

**Darwin:** `cmd/telemetron/setup.go:128` hard-fails on Darwin, but `install.sh:57` still advertises Darwin tarball downloads. An operator running `install.sh | sh` on macOS gets the binary but can't run `setup`. No scope statement anywhere.

**Non-systemd Linux** (containers, WSL1, Alpine openrc, etc.): `internal/service/service_linux.go:181` unconditionally calls `systemctl daemon-reload` / `enable --now`. Fails mysteriously without a clear precondition error.

**Fix (S):**
- `install.sh`: detect OS + init system early; if Darwin, print "binary installed; run `telemetron setup` manually — systemd service auto-setup is Linux-only."
- `setup`: probe for systemd (`systemctl --version` or `/run/systemd/system` existence). If absent, exit with explicit error: `telemetron setup requires systemd; detected init: <name>. Use 'telemetron install' + manual service management.`

### 🔴 5. Upgrade-in-place always restarts, contradicting "unchanged" contract

**New finding from Codex review.** `docs/setup-contract.md:68` promises idempotent behavior where unchanged state short-circuits without restart. Code at `cmd/telemetron/setup.go:225` always installs/updates and restarts — operator running `setup` a second time bounces the service unnecessarily.

**Fix (S-M):** Before the install step, compute a config-equality check against the current `/etc/telemetron/config.yaml`. If identical (including token file hash), emit `unchanged` phase event, skip service restart, exit 0.

### 🟠 6. Surface last HTTP response in health-check error

Today:
```
Error: health_check_failed: no successful flush observed within 60s
```
Desired:
```
Error: health_check_failed
  last attempt: POST .../v1/metrics → 403 forbidden_token_invalid
  hint: token may contain trailing whitespace; ensure file was written with `printf '%s'` not `echo`
```

**Codex upgraded this from 🟢 to 🟠** — more valuable than most other rough edges because it makes finding #2 actually debuggable.

### 🟠 7. `setup` performs partial writes on failure, no step-by-step visibility

When `setup` fails the health probe, it has already written config + restarted the service. Operator has no visible trace of what succeeded before the failure. Codex confirms full rollback is out of scope for v0.2.0 (would require transactional writes across `install.sh` + `service_linux.go:147`).

**Fix (S):** Print step-by-step progress so last-successful step is obvious:
```
[1/4] wrote /etc/telemetron/config.yaml
[2/4] reloaded systemd
[3/4] restarted telemetron.service (pid 28548)
[4/4] probing flush... failed (60s timeout)
```

### 🟠 8. Cobra dumps full `--help` on runtime error

Non-zero exit in `setup` prints the entire `setup --help` usage, obscuring the actual error.

**Fix (XS):** `SilenceUsage: true` on the cobra command, or guard usage printing behind arg-parse errors only.

### 🟠 9. `TELEMETRON_TOKEN_SECRET` env-based token fetch is undocumented

`install.sh` supports fetching tokens from AWS Secrets Manager via env, but `telemetron setup --help` only shows `--token-file`. No mention anywhere in the CLI output.

**Fix (XS):** Document the env var in `install.sh --help` and in the `setup` token-source summary:
```
token file: /etc/telemetron/token (source: SecretsManager /loki-telemetry/api-keys/openclaw)
```

**Codex rejected:** Adding `--token-secret` to `setup` itself. That pulls AWS-specific coupling into core CLI. Out of scope for v0.2.0.

### 🟠 10. Contradiction with original plan: installer auto-runs `setup`

**New finding from Codex review.** `docs/telemetron-setup-plan.md:351` says explicitly: *"No auto-running `setup` from `install.sh | sh` without consent."* But `install.sh:155` does exactly that when env vars are present.

**Decision needed:** Either (a) revise the original plan doc to bless the current auto-setup behavior, or (b) gate auto-setup behind an explicit env (`TELEMETRON_AUTO_SETUP=1`) and default to "binary installed; run `telemetron setup` next."

Probably (a) — the one-liner UX is valuable and has been de-facto accepted. But call it out in the CHANGELOG.

### 🟢 Deferred to v0.3.0

These stay on the radar but are out of scope for a patch release:

- **`telemetron doctor` subcommand** (diagnostic consolidation)
- **`install.sh --dry-run`** (preview mode)
- **Warm-up / connection priming** (superseded by #2's timeout increase)
- **Warn-only health mode** (contract change, not a patch)
- **`--token-secret` in `setup`** (AWS coupling in core CLI — needs design)
- **Rich endpoint preflight** (e.g. ping the authorizer before setup). Secret-fetch error handling in `install.sh:229` already exists; the real gap is better error surfaces covered by #6.
- **Full transactional rollback** (cross-file; needs design)

### Already covered (not a gap)

- **Corrupted binary download** — `install.sh:111` does sha256 verification. ✅

---

## What already works

- Zero-arg install with env vars (`curl | env ... sh`) produces a functional service
- Idempotent at the config-file level: re-running `setup` with different flags doesn't corrupt state
- sha256-verified tarball from GitHub Releases
- systemd unit is sound (correct Restart policy, auto-start on boot, proper User directive)
- Config file is readable YAML
- Flush logs are structured JSON
- `telemetron version` is machine-parseable
- Secrets Manager integration via env var works — just needs docs
- `$SUDO_USER`-based user detection already exists (I was wrong that it didn't)

---

## Implementation plan (v0.2.0)

Revised per Codex's priority order:

| # | Task | Sev | Effort | File(s) |
|---|------|-----|--------|---------|
| 1 | Fail-fast when session-dir / run-as unresolvable + persist both in `resolveInputs` | 🔴 | M (2h) | `cmd/telemetron/setup.go`, `internal/agentdetect/agentdetect.go` |
| 2 | Health timeout → 60s + `--health-timeout` + `TELEMETRON_HEALTH_TIMEOUT`; keep hard-fail | 🔴 | S (1h) | `cmd/telemetron/setup.go` |
| 3 | Unsupported-platform preconditions: Darwin auto-setup, non-systemd Linux | 🔴 | S (1h) | `install.sh`, `cmd/telemetron/setup.go`, `internal/service/` |
| 4 | `install.sh`: default HOME, drop blind `set -u` | 🔴 | XS (10m) | `install.sh` |
| 5 | Upgrade-in-place: skip service restart when config is unchanged | 🔴 | S-M (1-2h) | `cmd/telemetron/setup.go` |
| 6 | Surface last HTTP response in health-check error | 🟠 | S (1h) | `cmd/telemetron/setup.go`, `internal/otlp/exporter.go` |
| 7 | Step-by-step progress output + `SilenceUsage: true` | 🟠 | S (30m) | `cmd/telemetron/setup.go` |
| 8 | Document `TELEMETRON_TOKEN_SECRET` in `install.sh --help` + setup summary | 🟠 | XS (15m) | `install.sh`, `cmd/telemetron/setup.go` |
| 9 | Decide + document `install.sh` auto-setup contract | 🟠 | XS (15m) | `docs/telemetron-setup-plan.md`, `CHANGELOG.md` |

**Total effort:** ~7 hours of coding + tests.

## Test coverage required

- Unit: `resolveInputs` round-trip (session-dir + run-as persistence), ambiguous-detection fail paths, config-equality computation
- Integration: full `setup --yes --non-interactive` on Linux systemd happy path
- Integration: `setup` on Darwin → precondition error
- Integration: `setup` on a containerized init-less AL2023 → precondition error
- Integration: re-run `setup` with identical inputs → exits fast, no service bounce
- E2E: re-run the `openclaw-telemetron-e2e` skill on a fresh EC2 after the batch lands; should complete without manual fix-ups

## Rollout

1. Open GitHub issues on `inceptionstack/telemetron` for each 🔴 and the three 🟠 fixes — one issue per row above
2. Implement as one branch (or parallel branches if Codex handles them concurrently), each with tests
3. Tag **v0.2.0** with combined CHANGELOG entry:
   - Breaking: none (contract preserved)
   - Behavior change: new preconditions for Darwin + non-systemd Linux
   - Bug fixes: session-dir root detection, HOME unbound, always-restart regression
   - Enhancements: 60s health timeout, configurable, HTTP response in error, step-by-step output
4. Re-run `openclaw-telemetron-e2e` skill against a fresh box; gate release on clean pass
5. Add that skill to a nightly cron — regressions caught pre-release, ~4min + cents
