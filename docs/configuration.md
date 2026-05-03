# Configuration Reference

This document covers config keys, environment variables, CLI flags, and precedence rules for `clawtello`.

## Precedence

Configuration is assembled in layers:

1. `CLAWTELLO_CONFIG`, if set, decides which config file path is loaded.
2. Otherwise `--config` is used when provided.
3. Otherwise the platform default config path is used.
4. Values from the config file are loaded.
5. Supported environment variables override config file values.
6. Command-specific flag overrides are applied by the CLI.

Special cases:

- `CLAWTELLO_TOKEN` overrides `token_file` at runtime when `clawtello start` reads credentials.
- `clawtello install` resolves the token in this order: `--token`, `CLAWTELLO_TOKEN`, existing token file.
- `clawtello install` resolves `--endpoint` and `--mode` before the config load, so those flags win over the corresponding environment variables.

## Platform defaults

### Linux

- config: `/etc/clawtello/config.yaml`
- token: `/etc/clawtello/token`
- state dir: `/var/lib/clawtello`
- status file: `/var/lib/clawtello/status.json`

### macOS

- config: `~/.config/clawtello/config.yaml`
- token: `~/.config/clawtello/token`
- state dir: `~/.local/share/clawtello`
- status file: `~/.local/share/clawtello/status.json`

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
- Required: yes unless `CLAWTELLO_TOKEN` is set
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
  - Linux: `/var/lib/clawtello/openclaw.state.json`
  - macOS: `~/.local/share/clawtello/openclaw.state.json`
- Purpose: durable offset store for session tailing

## Environment variables

### `CLAWTELLO_CONFIG`

- Overrides the config file path selection

### `CLAWTELLO_ENDPOINT`

- Overrides `endpoint`

### `CLAWTELLO_TOKEN`

- Inline bearer token
- Used at runtime instead of `token_file` when set

### `CLAWTELLO_TOKEN_FILE`

- Overrides `token_file`

### `CLAWTELLO_MODE`

- Overrides `mode`

### `CLAWTELLO_LOG_LEVEL`

- Overrides `log_level`

## CLI commands and flags

## Global flags

### `--config`

- Applies to: all commands
- Purpose: choose config file path when `CLAWTELLO_CONFIG` is not set

### `--log-level`

- Applies to: `start`, `status`, `install`
- Purpose: override `log_level`

## `clawtello install`

Installs and starts the Linux systemd service. Unsupported on macOS and other non-Linux platforms.

Flags:

- `--endpoint`: OTLP endpoint override
- `--token`: bearer token to write to `token_file`
- `--mode`: collector mode override
- `--deployment-id`: sets `declared.deployment_id`
- `--tier`: sets `declared.tier`
- `--session-dir`: sets `<mode>.session_dir`
- `--insecure-endpoint`: allows `http://` endpoints for local testing

## `clawtello start`

Runs the collector in the foreground using the resolved config and token.

## `clawtello status`

Prints service status and the last local status-file snapshot without making network calls.

## `clawtello uninstall`

Removes the installed Linux service unit but leaves config and state on disk.

## `clawtello version`

Prints build metadata: version, commit, date, OS, and architecture.

## Example config

```yaml
mode: openclaw
endpoint: https://your-otlp-gateway.example.com/v1/metrics
token_file: /etc/clawtello/token
log_level: info
insecure_endpoint: false

declared:
  deployment_id: dev-laptop
  tier: development
  environment: local
  pack_version: clawtello-0.2.0

openclaw:
  session_dir: /home/you/.openclaw/agents/main/sessions
  flush_interval: 15s
  scan_interval: 15s
  state_file: /var/lib/clawtello/openclaw.state.json
```
