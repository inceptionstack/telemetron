# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`telemetron setup` subcommand** — non-interactive-first reconciler that
  auto-detects OpenClaw/Loki agents, resolves inputs from flags/env/
  existing state, installs or updates the systemd unit, and verifies the
  first flush before returning success. Interactive prompting is
  available as a fallback only when stdin is a TTY and
  `--non-interactive` was not passed. See `docs/setup-contract.md`.
- **`--json` event stream for `setup`** (contract:
  `telemetron.setup.v1`) — one line-delimited JSON event per lifecycle
  phase, plus a final `setup.completed` / `setup.failed` envelope. Stable
  error codes: `missing_required_input`, `ambiguous_agent`,
  `token_read_failed`, `systemd_install_failed`, `service_start_failed`,
  `health_check_failed`, `precondition_failed`, `detection_failed`,
  `invalid_config`.
- **`internal/agentdetect` package** — filesystem-only, network-free
  agent detector (OpenClaw today, pluggable for future agents). Prefers
  the `main` agent slot; flags ambiguity when multiple slots exist and
  no `main` is present.
- **`--token-file` on `telemetron install`** — reads the token from a
  path instead of the CLI. The CLI form is never written to argv.
- **`TELEMETRON_TOKEN_FILE` env var** — recognised by both `install` and
  `setup`.

### Changed

- `telemetron install` now delegates to a code path that shares
  resolution logic with `setup`; behaviour is backwards compatible.
- README quickstart leads with `telemetron setup` and demotes the
  manual token/install dance to a "power users / CI" section.

### Deprecated

- `telemetron install --token <value>` — emits a runtime warning. Leaks
  via shell history and `/proc/<pid>/cmdline`. Use `--token-file`,
  `TELEMETRON_TOKEN`, or `telemetron setup` (with interactive hidden
  prompt as fallback). The flag will be removed in a future minor
  release.

### Notes

- The systemd unit still defaults `User=`/`Group=` to `$SUDO_USER` (or
  the explicit `--run-as` user), falling back to the dedicated
  `telemetron` system user only when `$SUDO_USER` is unset. This
  behaviour is unchanged from the previous unreleased entry.

## [0.2.0] - 2026-05-02

### Changed

- Refactored collector registration to self-registering specs with per-mode config decoding and a build-tagged demo collector.
- Split daemon management into a build-tagged `internal/service` layer, added Linux install ownership fixes, and improved status, version, and install CLI behavior.
- Hardened config, collector, OTLP sink, registry, and service test coverage while removing duplicated file-write and JSON helper logic.

## [0.1.0]

### Added

- Scaffolded the `telemetron` Go sidecar with `openclaw` collection mode.
- Added config loading, durable offsets, OTLP/HTTP protobuf export, systemd lifecycle, and status reporting.
- Added unit tests, Makefile targets, and GitHub Actions CI for test, lint, and module verification.
