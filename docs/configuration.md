# Configuration Reference

This document covers config keys, environment variables, CLI flags, and precedence rules for `telemetron`.

## Precedence

Configuration is assembled in layers:

1. `TELEMETRON_CONFIG`, if set, decides which config file path is loaded.
2. Otherwise `--config` is used when provided.
3. Otherwise the platform default config path is used.
4. Values from the config file are loaded.
5. Supported environment variables override config file values.
6. Command-specific flag overrides are applied by the CLI.

Special cases:

- `TELEMETRON_TOKEN` overrides `token_file` at runtime when `telemetron start` reads credentials.
- `TELEMETRON_TOKEN_FILE` overrides `token_file` via config load; the resolved path is then read at runtime unless `TELEMETRON_TOKEN` is also set (which wins).
- `telemetron install` resolves the token in this order: `--token`, `TELEMETRON_TOKEN`, existing `token_file` (path resolved from config, `--token-file` equivalent via `TELEMETRON_TOKEN_FILE`, or the platform default).
- `telemetron install` resolves `--endpoint` and `--mode` before the config load, so those flags win over the corresponding environment variables.

## Platform defaults

### Linux

- config: `/etc/telemetron/config.yaml`
- token: `/etc/telemetron/token`
- state dir: `/var/lib/telemetron`
- status file: `/var/lib/telemetron/status.json`

### macOS

- config: `~/.config/telemetron/config.yaml`
- token: `~/.config/telemetron/token`
- state dir: `~/.local/share/telemetron`
- status file: `~/.local/share/telemetron/status.json`

## Top-level config keys

### `mode`

- Type: string
- Required: yes
- Current values: `openclaw`
- Purpose: selects the collector package to load

### `endpoint`

- Type: string
- Required: yes
- Purpose: OTLP/HTTP metrics endpoint
- Validation: must start with `https://` unless `insecure_endpoint: true`

### `token_file`

- Type: string
- Required: yes unless `TELEMETRON_TOKEN` is set
- Purpose: file containing the bearer token
- Recommended permissions: `0400`

### `log_level`

- Type: string
- Required: no
- Default: `info`
- Purpose: structured log verbosity

### `insecure_endpoint`

- Type: boolean
- Required: no
- Default: `false`
- Purpose: allow plaintext `http://` endpoints for local testing

### `declared`

Optional operator-supplied metadata attached as OTLP resource attributes for local observability. Servers may strip and replace these values.

#### `declared.deployment_id`

- Type: string
- Purpose: operator-defined deployment identifier

#### `declared.tier`

- Type: string
- Purpose: operator-defined tier label such as `development` or `production`

#### `declared.environment`

- Type: string
- Purpose: operator-defined environment label

#### `declared.pack_version`

- Type: string
- Purpose: operator-defined build or rollout label

## `openclaw` collector config

### `openclaw.session_dir`

- Type: string
- Required: yes
- Purpose: directory containing session `*.jsonl` files
- Default resolution:
  - Linux: `$HOME/.openclaw/agents/main/sessions` when `HOME` is set
  - macOS: `$HOME/.openclaw/agents/main/sessions` when `HOME` is set
  - if `HOME` is unavailable, the value is empty and must be supplied explicitly

### `openclaw.flush_interval`

- Type: duration string
- Required: yes
- Default: `15s`
- Purpose: OTLP flush cadence

### `openclaw.scan_interval`

- Type: duration string
- Required: yes
- Default: `15s`
- Purpose: directory rescan cadence

### `openclaw.state_file`

- Type: string
- Required: yes
- Default:
  - Linux: `/var/lib/telemetron/openclaw.state.json`
  - macOS: `~/.local/share/telemetron/openclaw.state.json`
- Purpose: durable offset store for session tailing

## Environment variables

### `TELEMETRON_CONFIG`

- Overrides the config file path selection

### `TELEMETRON_ENDPOINT`

- Overrides `endpoint`

### `TELEMETRON_TOKEN`

- Inline bearer token
- Used at runtime instead of `token_file` when set

### `TELEMETRON_TOKEN_FILE`

- Overrides `token_file`

### `TELEMETRON_MODE`

- Overrides `mode`

### `TELEMETRON_LOG_LEVEL`

- Overrides `log_level`

## CLI commands and flags

## Global flags

### `--config`

- Applies to: all commands
- Purpose: choose config file path when `TELEMETRON_CONFIG` is not set

### `--log-level`

- Applies to: `start`, `status`, `install`
- Purpose: override `log_level`

## `telemetron install`

Installs and starts the Linux systemd service. Unsupported on macOS and other non-Linux platforms.

Flags:

- `--endpoint`: OTLP endpoint override
- `--token`: bearer token to write to `token_file`
- `--mode`: collector mode override
- `--deployment-id`: sets `declared.deployment_id`
- `--tier`: sets `declared.tier`
- `--session-dir`: sets `<mode>.session_dir`
- `--insecure-endpoint`: allows `http://` endpoints for local testing

## `telemetron start`

Runs the collector in the foreground using the resolved config and token.

## `telemetron status`

Prints service status and the last local status-file snapshot without making network calls.

## `telemetron uninstall`

Removes the installed Linux service unit but leaves config and state on disk.

## `telemetron version`

Prints build metadata: version, commit, date, OS, and architecture.

## Example config

```yaml
mode: openclaw
endpoint: https://your-otlp-gateway.example.com/v1/metrics
token_file: /etc/telemetron/token
log_level: info
insecure_endpoint: false

declared:
  deployment_id: dev-laptop
  tier: development
  environment: local
  pack_version: telemetron-0.2.0

openclaw:
  session_dir: /home/you/.openclaw/agents/main/sessions
  flush_interval: 15s
  scan_interval: 15s
  state_file: /var/lib/telemetron/openclaw.state.json
```
