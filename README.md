# telemetron

Privacy-safe telemetry sidecar for coding agent sessions. Exports bounded OTLP metrics without shipping transcript content.

[![CI](https://img.shields.io/github/actions/workflow/status/inceptionstack/telemetron/ci.yml?branch=main&label=ci)](https://github.com/inceptionstack/telemetron/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/inceptionstack/telemetron)](LICENSE)

## Install

### Quick install (with auto-enrollment)

For most users — installs the binary, auto-enrolls with the telemetry endpoint, and starts the systemd service:

```bash
curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | \
  TELEMETRON_ENDPOINT=https://your-endpoint.example.com/v1/metrics \
  TELEMETRON_ENROLL_ENDPOINT=https://your-endpoint.example.com/v1/enroll \
  TELEMETRON_MODE=openclaw \
  sudo -E bash
```

This will:
1. Download and verify the latest binary
2. Auto-enroll with the telemetry backend (no token needed)
3. Detect your agent's session directory
4. Install and start a systemd service

### With an existing token

If you already have a bearer token:

```bash
curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | \
  TELEMETRON_ENDPOINT=https://your-endpoint.example.com/v1/metrics \
  TELEMETRON_TOKEN_FILE=/path/to/token \
  TELEMETRON_MODE=openclaw \
  sudo -E bash
```

### Binary only (no service)

```bash
curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | sh
```

### Environment variables

| Variable | Required | Description |
|----------|----------|-------------|
| `TELEMETRON_ENDPOINT` | Yes (for service) | OTLP/HTTP metrics endpoint (`/v1/metrics`) |
| `TELEMETRON_ENROLL_ENDPOINT` | For auto-enroll | Enrollment endpoint (`/v1/enroll`). Defaults to `https://telemetry.loki.run/v1/enroll` |
| `TELEMETRON_MODE` | Recommended | Agent mode: `openclaw`, `claude-code`, `kiro-cli`, etc. |
| `TELEMETRON_TOKEN_FILE` | If not enrolling | Path to bearer token file |
| `TELEMETRON_TOKEN_SECRET` | If not enrolling | AWS Secrets Manager secret ID |
| `TELEMETRON_VERSION` | No | Pin a specific release (default: latest) |
| `TELEMETRON_PREFIX` | No | Install root (default: `$HOME/.local`, or `/usr/local` under sudo) |
| `TELEMETRON_SESSION_DIR` | No | Override auto-detected session directory |
| `TELEMETRON_RUN_AS` | No | User to run the service as (default: `$SUDO_USER`) |
| `TELEMETRON_NO_AUTO_ENROLL` | No | Set to `1` to disable anonymous enrollment |
| `TELEMETRON_SETUP_ARGS` | No | Extra args passed to `telemetron setup` |

### Other install methods

```bash
# From source
git clone https://github.com/inceptionstack/telemetron.git
cd telemetron && make build

# Go install
go install github.com/inceptionstack/telemetron/cmd/telemetron@latest
```

## What it does

`telemetron` watches local agent session files, derives a bounded set of counters (session starts, agent turns, tool calls, errors), and exports them via OTLP/HTTP. It never sends transcript bodies, prompt content, or tool arguments.

```
sessions/*.jsonl → tail → derive counters → OTLP/HTTP → your gateway
```

### Metrics exported

| Metric | Type | Description |
|--------|------|-------------|
| `pack.session.start` | counter | Session started |
| `pack.agent.turn` | counter | Agent turn completed |
| `pack.tool.call` | counter | Tool invocation |
| `pack.error` | counter | Error occurred |
| `pack.emitter.heartbeat` | counter | Periodic liveness signal |

### Privacy

- Only allowlisted metric names and normalized attribute values are sent
- No transcript content, prompts, responses, or tool payloads leave the machine
- Bearer token stored in `0400` file; HTTPS required by default
- See [docs/privacy.md](docs/privacy.md) for full details

## Configuration

After install, config lives at `/etc/telemetron/config.yaml`:

```yaml
mode: openclaw
endpoint: https://your-endpoint.example.com/v1/metrics
token_file: /etc/telemetron/token
log_level: info
run_as: your-user

declared:
  deployment_id: loki@my-host
  tier: external

openclaw:
  session_dir: /home/your-user/.openclaw/agents/main/sessions
  flush_interval: 15s
  scan_interval: 15s
  state_file: /var/lib/telemetron/openclaw.state.json
```

Full reference: [docs/configuration.md](docs/configuration.md)

## Managing the service

```bash
# Check status
sudo telemetron status

# Re-run setup (idempotent — same inputs = no-op)
sudo telemetron setup --endpoint ... --token-file ...

# Uninstall
sudo telemetron uninstall
sudo rm -rf /etc/telemetron /var/lib/telemetron  # optional: remove config + state
```

## Disabling telemetry

Any of these opt-out signals will prevent telemetron from starting:

- `DO_NOT_TRACK=1` — [consoledonottrack.com](https://consoledonottrack.com) standard
- `TELEMETRON_TELEMETRY=0`
- `~/.telemetron/telemetry-off` marker file
- `LOWKEY_TELEMETRY=0` (when deployed via [lowkey](https://github.com/inceptionstack/lowkey))

## Extending to other agents

The collector interface is additive — add a new collector package for your agent format without touching the OTLP sink or service plumbing. See [docs/extending.md](docs/extending.md).

## Platform support

| Platform | Service install | Foreground | Status |
|----------|----------------|------------|--------|
| Linux (systemd) | ✅ | ✅ | ✅ |
| macOS | ❌ (use launchd) | ✅ | ✅ |

## License

Apache-2.0 — see [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Security

See [SECURITY.md](SECURITY.md). Do **not** file public issues for token exposure or telemetry content leakage.
