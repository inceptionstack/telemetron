# clawtello

Privacy-safe telemetry sidecar for Loki/OpenClaw-style agent sessions, exporting bounded OTLP metrics without shipping transcript content.

[![Go Reference](https://pkg.go.dev/badge/github.com/inceptionstack/clawtello.svg)](https://pkg.go.dev/github.com/inceptionstack/clawtello)
[![CI](https://img.shields.io/github/actions/workflow/status/inceptionstack/clawtello/ci.yml?branch=main&label=ci)](https://github.com/inceptionstack/clawtello/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/inceptionstack/clawtello)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/inceptionstack/clawtello)](go.mod)

## What it is

`clawtello` is a small Go binary that tails local session JSONL files, derives a bounded set of counters, and exports them to any OTLP/HTTP metrics endpoint. It is built for operators who want host-local telemetry collection without shipping prompts, responses, tool payloads, or other per-message content off the machine.

## Why

Modern coding agents leave rich local state behind, but shipping that state raw creates privacy, compliance, and operational risk. `clawtello` solves that by acting as a privacy-safe telemetry sidecar that derives bounded counters and sends them to any OTLP/HTTP endpoint with bearer auth, without leaking per-message content or prompt data.

## Install

1. Go install:

   ```bash
   go install github.com/inceptionstack/clawtello/cmd/clawtello@latest
   ```

2. Prebuilt binaries:

   Download the matching archive from GitHub Releases for:

   - `linux/amd64`
   - `linux/arm64`
   - `darwin/amd64`
   - `darwin/arm64`

3. From source:

   ```bash
   git clone https://github.com/inceptionstack/clawtello.git
   cd clawtello
   make build
   ```

## Quickstart

This example installs `clawtello` against a generic OTLP/HTTP endpoint. Replace the token value and session directory for your host.

```bash
git clone https://github.com/inceptionstack/clawtello.git
cd clawtello
make build

sudo install -d -m 0755 /etc/clawtello
printf '%s\n' 'replace-with-your-bearer-token' | sudo tee /etc/clawtello/token >/dev/null
sudo chmod 0400 /etc/clawtello/token

# The install command reads the token from /etc/clawtello/token by default.
# Avoid passing --token on the CLI in production; it leaks into shell
# history and the kernel process list.
sudo ./clawtello install \
  --endpoint https://your-otlp-gateway.example.com/v1/metrics \
  --mode openclaw \
  --session-dir "$HOME/.openclaw/agents/main/sessions" \
  --deployment-id dev-laptop \
  --tier development

./clawtello status
```

For macOS, run `clawtello start --config ~/.config/clawtello/config.yaml` directly or under `launchd`. See [docs/macos.md](docs/macos.md).

## Configuration

The full reference lives in [docs/configuration.md](docs/configuration.md). A complete example:

```yaml
# Collector mode to load. v0.2 ships with "openclaw".
mode: openclaw

# OTLP/HTTP metrics endpoint. Use HTTPS in production.
endpoint: https://your-otlp-gateway.example.com/v1/metrics

# Path to a bearer token file. Keep this file mode 0400.
token_file: /etc/clawtello/token

# Structured log verbosity for foreground runs and service logs.
log_level: info

# Allow plaintext http:// endpoints for local testing only.
insecure_endpoint: false

declared:
  # Optional local metadata for debugging. The server may override identity.
  deployment_id: dev-laptop
  # Optional deployment tier hint.
  tier: development
  # Optional environment hint.
  environment: local
  # Optional operator-supplied pack or bundle version.
  pack_version: clawtello-0.2.0

openclaw:
  # Directory containing session JSONL files to tail.
  session_dir: /home/you/.openclaw/agents/main/sessions
  # Interval between OTLP flushes.
  flush_interval: 15s
  # Interval between scans for appended session data.
  scan_interval: 15s
  # Durable state file storing per-session offsets.
  state_file: /var/lib/clawtello/openclaw.state.json
```

## How it works

`clawtello` watches local session files, resumes from durable offsets, derives an allowlisted set of counters, and exports those counters to your OTLP/HTTP gateway with bearer authentication. The collector never sends transcript bodies, prompt content, tool arguments, or other raw session payloads. It ships only bounded metric names and normalized attribute values.

```text
sessions/*.jsonl
       |
       v
     tail
       |
       v
derive counters
       |
       v
   OTLP/HTTP
       |
       v
 your gateway
```

## Extending to other agents

The collector interface is intentionally additive. If you need support for another local agent or session format, you can add a new collector package, register it, and ship it without touching the OTLP sink or service plumbing. See [docs/extending.md](docs/extending.md) for a five-step walkthrough using a `claude-code` example.

## Platform support

| Platform | Install | Start | Status |
| --- | --- | --- | --- |
| Linux systemd | `clawtello install` supported | `clawtello start` supported | `clawtello status` supported |
| macOS | daemon install unsupported | foreground `start` supported | `status` supported |
| Other | daemon install unsupported | best-effort foreground `start` | limited `status` detail |

## Security

`clawtello` is designed so message content never leaves the box. The collector emits only allowlisted metric names with a bounded attribute set, reads its bearer token from a `0400` file, and requires HTTPS by default. Plaintext endpoints are rejected unless `insecure_endpoint` is explicitly enabled for local testing.

## Disabling telemetry

`clawtello` honors its own opt-out signals **and** the [`lowkey`](https://github.com/inceptionstack/lowkey) installer-family signals, so a single opt-out disables both tools when they are deployed together:

**Shared**
- `DO_NOT_TRACK=1` — [consoledonottrack.com](https://consoledonottrack.com) community standard. Truthy: `1|true|yes|on` (case-insensitive).

**clawtello-specific**
- `CLAWTELLO_TELEMETRY=0` — falsy values `0|false|no|off` opt out; unset or any other value keeps telemetry enabled.
- `~/.clawtello/telemetry-off` — marker file under the service user’s home.

**Lowkey-family (inherited when deployed via `lowkey`)**
- `LOWKEY_TELEMETRY=0`
- `~/.lowkey/telemetry-off`

When any signal is present, `clawtello start` exits cleanly without loading config, reading the token, or opening any sockets. `clawtello status` reports `telemetry: disabled (<source>)` instead of probing the service.

Note: environment variables set in the interactive shell that ran the `lowkey` installer do **not** propagate into the `clawtello` systemd unit. For `lowkey`-installed deployments, `lowkey` should drop the marker file into the `clawtello` service user’s home so the opt-out sticks across restarts.

## Status

`alpha` for `v0.2`.

## License

Apache-2.0. See [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
