# telemetron

Privacy-safe telemetry sidecar for coding agent sessions. Exports bounded OTLP metrics without shipping transcript content.

[![CI](https://img.shields.io/github/actions/workflow/status/inceptionstack/telemetron/ci.yml?branch=main&label=ci)](https://github.com/inceptionstack/telemetron/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/inceptionstack/telemetron)](LICENSE)

## Install

### Quick install (auto-detect + auto-enroll)

For machines running **roundhouse** or **openclaw** — installs the binary, auto-detects your agent, enrolls, and starts the systemd service:

```bash
curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | \
  TELEMETRON_PREFIX=/usr/local sudo -E bash
sudo telemetron detect \
  --endpoint https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/metrics \
  --force
```

This will:
1. Download and install the latest binary
2. Auto-detect which agent packs are on the machine (roundhouse, openclaw)
3. Auto-enroll with the telemetry backend (no token needed)
4. Install and start a systemd service per detected pack

### Single-pack install (roundhouse)

```bash
curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | \
  TELEMETRON_PREFIX=/usr/local sudo -E bash
sudo telemetron detect \
  --endpoint https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/metrics \
  --mode roundhouse \
  --force
```

### Single-pack install (openclaw)

```bash
curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | \
  TELEMETRON_PREFIX=/usr/local sudo -E bash
sudo telemetron detect \
  --endpoint https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/metrics \
  --mode openclaw \
  --force
```

### One-liner (legacy, uses install.sh setup)

```bash
curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | \
  TELEMETRON_ENDPOINT=https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/metrics \
  TELEMETRON_MODE=openclaw \
  sudo -E bash
```

### Binary only (no service)

```bash
curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | bash
```

### Prerequisites

- Linux with systemd (for service install)
- The agent's session directory must exist (e.g., `~/.roundhouse/sessions/main/` or `~/.openclaw/agents/main/sessions/`)
- If the session directory doesn't exist yet (fresh install, no messages received), create it as the agent user:
  ```bash
  # Replace <user> with the user running the agent (e.g., ec2-user, ubuntu)
  sudo -u <user> mkdir -p /home/<user>/.roundhouse/sessions/main   # for roundhouse
  sudo -u <user> mkdir -p /home/<user>/.openclaw/agents/main/sessions  # for openclaw
  ```

### Environment variables

These are read by the telemetron binary (via `install.sh` subprocess or direct invocation):

| Variable | Required | Description |
|----------|----------|-------------|
| `TELEMETRON_ENDPOINT` | For auto-setup | OTLP/HTTP metrics endpoint (`/v1/metrics`) |
| `TELEMETRON_ENROLL_ENDPOINT` | No | Override enrollment endpoint (default is built-in) |
| `TELEMETRON_MODE` | Recommended | Agent mode: `openclaw`, `roundhouse` |
| `TELEMETRON_TOKEN_FILE` | If not enrolling | Path to bearer token file |
| `TELEMETRON_TOKEN_SECRET` | If not enrolling | AWS Secrets Manager secret ID |
| `TELEMETRON_VERSION` | No | Pin a specific release (default: latest) |
| `TELEMETRON_PREFIX` | No | Install root (default: `$HOME/.local`, or `/usr/local` under sudo) |
| `TELEMETRON_SESSION_DIR` | No | Override auto-detected session directory |
| `TELEMETRON_RUN_AS` | No | User to run the service as (default: `$SUDO_USER`) |
| `TELEMETRON_NO_AUTO_ENROLL` | No | Set to `1` to disable anonymous enrollment |
| `TELEMETRON_SETUP_ARGS` | No | Extra args passed to `telemetron setup` (legacy path only) |

### Other install methods

```bash
# From source
git clone https://github.com/inceptionstack/telemetron.git
cd telemetron && go build ./cmd/telemetron

# Go install
go install github.com/inceptionstack/telemetron/cmd/telemetron@latest
```

## Commands

### `telemetron detect`

Auto-detect all agent packs on the machine and configure telemetron for each:

```bash
sudo telemetron detect \
  --endpoint https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/metrics \
  --force
```

Flags:
- `--endpoint` — metrics endpoint (required)
- `--enroll-endpoint` — override enrollment endpoint (optional, default built-in)
- `--mode <name>` — only configure a specific pack (e.g., `roundhouse`)
- `--force` — reconfigure even if already set up

On multi-pack hosts, `detect` assigns openclaw as primary and other packs as secondary instances with their own systemd units.

### `telemetron setup`

Manual setup for a single pack (lower-level than `detect`):

```bash
sudo telemetron setup \
  --endpoint https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/metrics \
  --mode roundhouse \
  --session-dir /home/ec2-user/.roundhouse/sessions/main \
  --run-as ec2-user
```

### `telemetron status`

```bash
sudo telemetron status
```

### `telemetron update`

Manually update to the latest release:

```bash
sudo telemetron update
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

# Re-detect and reconfigure
sudo telemetron detect \
  --endpoint https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/metrics \
  --force

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
