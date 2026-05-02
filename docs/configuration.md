# Configuration Reference

This document covers config keys, environment variables, CLI flags, and precedence rules for `lokiotel`.

## Precedence

Configuration is assembled in layers:

1. `LOKIOTEL_CONFIG`, if set, decides which config file path is loaded.
2. Otherwise `--config` is used when provided.
3. Otherwise the platform default config path is used.
4. Values from the config file are loaded.
5. Supported environment variables override config file values.
6. Command-specific flag overrides are applied by the CLI.

Special cases:

- `LOKIOTEL_TOKEN` overrides `token_file` at runtime when `lokiotel start` reads credentials.
- `lokiotel install` resolves the token in this order: `--token`, `LOKIOTEL_TOKEN`, existing token file.
- `lokiotel install` resolves `--endpoint` and `--mode` before the config load, so those flags win over the corresponding environment variables.

## Platform defaults

### Linux

- config: `/etc/lokiotel/config.yaml`
- token: `/etc/lokiotel/token`
- state dir: `/var/lib/lokiotel`
- status file: `/var/lib/lokiotel/status.json`

### macOS

- config: `~/.config/lokiotel/config.yaml`
- token: `~/.config/lokiotel/token`
- state dir: `~/.local/share/lokiotel`
- status file: `~/.local/share/lokiotel/status.json`

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
- Required: yes unless `LOKIOTEL_TOKEN` is set
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
  - Linux: `/var/lib/lokiotel/openclaw.state.json`
  - macOS: `~/.local/share/lokiotel/openclaw.state.json`
- Purpose: durable offset store for session tailing

## Environment variables

### `LOKIOTEL_CONFIG`

- Overrides the config file path selection

### `LOKIOTEL_ENDPOINT`

- Overrides `endpoint`

### `LOKIOTEL_TOKEN`

- Inline bearer token
- Used at runtime instead of `token_file` when set

### `LOKIOTEL_TOKEN_FILE`

- Overrides `token_file`

### `LOKIOTEL_MODE`

- Overrides `mode`

### `LOKIOTEL_LOG_LEVEL`

- Overrides `log_level`

## CLI commands and flags

## Global flags

### `--config`

- Applies to: all commands
- Purpose: choose config file path when `LOKIOTEL_CONFIG` is not set

### `--log-level`

- Applies to: `start`, `status`, `install`
- Purpose: override `log_level`

## `lokiotel install`

Installs and starts the Linux systemd service. Unsupported on macOS and other non-Linux platforms.

Flags:

- `--endpoint`: OTLP endpoint override
- `--token`: bearer token to write to `token_file`
- `--mode`: collector mode override
- `--deployment-id`: sets `declared.deployment_id`
- `--tier`: sets `declared.tier`
- `--session-dir`: sets `<mode>.session_dir`
- `--insecure-endpoint`: allows `http://` endpoints for local testing

## `lokiotel start`

Runs the collector in the foreground using the resolved config and token.

## `lokiotel status`

Prints service status and the last local status-file snapshot without making network calls.

## `lokiotel uninstall`

Removes the installed Linux service unit but leaves config and state on disk.

## `lokiotel version`

Prints build metadata: version, commit, date, OS, and architecture.

## Example config

```yaml
mode: openclaw
endpoint: https://your-otlp-gateway.example.com/v1/metrics
token_file: /etc/lokiotel/token
log_level: info
insecure_endpoint: false

declared:
  deployment_id: dev-laptop
  tier: development
  environment: local
  pack_version: lokiotel-0.2.0

openclaw:
  session_dir: /home/you/.openclaw/agents/main/sessions
  flush_interval: 15s
  scan_interval: 15s
  state_file: /var/lib/lokiotel/openclaw.state.json
```
