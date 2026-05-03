# telemetron

Privacy-safe telemetry sidecar for Loki/OpenClaw-style agent sessions, exporting bounded OTLP metrics without shipping transcript content.

[![Go Reference](https://pkg.go.dev/badge/github.com/inceptionstack/telemetron.svg)](https://pkg.go.dev/github.com/inceptionstack/telemetron)
[![CI](https://img.shields.io/github/actions/workflow/status/inceptionstack/telemetron/ci.yml?branch=main&label=ci)](https://github.com/inceptionstack/telemetron/actions/workflows/ci.yml)
[![License](https://img.shields.io/github/license/inceptionstack/telemetron)](LICENSE)
[![Go Version](https://img.shields.io/github/go-mod/go-version/inceptionstack/telemetron)](go.mod)

## What it is

`telemetron` is a small Go binary that tails local session JSONL files, derives a bounded set of counters, and exports them to any OTLP/HTTP metrics endpoint. It is built for operators who want host-local telemetry collection without shipping prompts, responses, tool payloads, or other per-message content off the machine.

## Why

Modern coding agents leave rich local state behind, but shipping that state raw creates privacy, compliance, and operational risk. `telemetron` solves that by acting as a privacy-safe telemetry sidecar that derives bounded counters and sends them to any OTLP/HTTP endpoint with bearer auth, without leaking per-message content or prompt data.

## Install

### One-line installer (recommended)

Downloads the latest Linux/macOS (amd64/arm64) release tarball from GitHub,
verifies its SHA-256 against the published `checksums.txt`, and installs the
binary into `$HOME/.local/bin/telemetron`:

```bash
curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | sh
```

The script is [POSIX shell](install.sh) and uses only `curl`, `tar`,
`sha256sum` (or `shasum`), and `uname`. It does **not** configure the systemd
service; for that, run `telemetron install --help` after the binary is on
your `PATH`.

Environment overrides:

- `TELEMETRON_VERSION` — pin a specific release tag (default: latest)
- `TELEMETRON_PREFIX` — install root (default: `$HOME/.local`)

### Alternatives

1. `go install`:

   ```bash
   go install github.com/inceptionstack/telemetron/cmd/telemetron@latest
   ```

2. Manual download from [Releases](https://github.com/inceptionstack/telemetron/releases):

   - `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`

3. From source:

   ```bash
   git clone https://github.com/inceptionstack/telemetron.git
   cd telemetron
   make build
   ```

## Quickstart

After `install.sh` (or any of the alternatives above) puts `telemetron` on
your `PATH`, use `telemetron setup` to wire it up. It auto-detects your
agent, resolves sensible defaults, and verifies a first flush before
reporting success.

### Interactive (laptop, one-off)

Drop the bearer token somewhere readable, then:

```bash
sudo telemetron setup \
  --endpoint https://your-otlp-gateway.example.com/v1/metrics \
  --token-file /path/to/your/token
```

What happens:

1. Detects `~/.openclaw/agents/*/sessions` for the user who invoked `sudo`
   (prefers `main` when present) and picks sensible `mode`, `session-dir`,
   `run-as`, `deployment-id`, and `tier` defaults.
2. Prints a summary of the resolved state and asks `Proceed? [Y/n]`.
3. Installs the systemd unit, enables it, starts it, and waits for the
   first successful flush before printing `telemetron installed — first
   flush ok`.

Re-running `telemetron setup` is always safe: same inputs → no-op; changed
endpoint or token → in-place update + service restart.

### Non-interactive (bundled installer, CI, image bake)

`setup` is non-interactive-first. The same reconciler runs with zero
prompts when stdin is not a TTY, or when `--non-interactive` is passed:

```bash
TELEMETRON_ENDPOINT="https://your-otlp-gateway.example.com/v1/metrics" \
TELEMETRON_TOKEN_FILE=/run/secrets/telemetron-token \
sudo telemetron setup --non-interactive --json --yes
```

`--json` emits one line-delimited JSON event per lifecycle phase on
stdout, plus a final `setup.completed` or `setup.failed` envelope. The
schema is stable (`telemetron.setup.v1`); see
[docs/setup-contract.md](docs/setup-contract.md) for the full event list
and error codes.

Missing required inputs (`endpoint`, `token`) and ambiguous detection
(more than one agent slot without `main`) fail fast with a structured
error envelope — never silent fallbacks.

### Low-level install (power users)

`telemetron install` remains available for CI or tooling that already
scripts against it. Prefer `setup` for everything else.

```bash
sudo telemetron install \
  --endpoint https://your-otlp-gateway.example.com/v1/metrics \
  --token-file /etc/telemetron/token \
  --mode openclaw \
  --session-dir "$HOME/.openclaw/agents/main/sessions"
```

The `--token` flag is deprecated (leaks via `ps` and shell history) and
will be removed in a future release; use `--token-file` instead.

For macOS, run `telemetron start --config ~/.config/telemetron/config.yaml`
directly or under `launchd`. See [docs/macos.md](docs/macos.md).

## Uninstall

If you installed via the one-line installer, remove the binary:

```bash
rm -f "$HOME/.local/bin/telemetron"
```

If you installed the systemd service (via `telemetron setup` or
`telemetron install`), remove it first (the config and state files are
left on disk for safety):

```bash
sudo telemetron uninstall               # stop + disable + remove the unit
sudo rm -rf /etc/telemetron             # config + token (optional)
sudo rm -rf /var/lib/telemetron         # durable offsets + status (optional)
```
## Configuration

The full reference lives in [docs/configuration.md](docs/configuration.md). A complete example:

```yaml
# Collector mode to load. v0.2 ships with "openclaw".
mode: openclaw

# OTLP/HTTP metrics endpoint. Use HTTPS in production.
endpoint: https://your-otlp-gateway.example.com/v1/metrics

# Path to a bearer token file. Keep this file mode 0400.
token_file: /etc/telemetron/token

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
  pack_version: telemetron-0.2.0

openclaw:
  # Directory containing session JSONL files to tail.
  session_dir: /home/you/.openclaw/agents/main/sessions
  # Interval between OTLP flushes.
  flush_interval: 15s
  # Interval between scans for appended session data.
  scan_interval: 15s
  # Durable state file storing per-session offsets.
  state_file: /var/lib/telemetron/openclaw.state.json
```

## How it works

`telemetron` watches local session files, resumes from durable offsets, derives an allowlisted set of counters, and exports those counters to your OTLP/HTTP gateway with bearer authentication. The collector never sends transcript bodies, prompt content, tool arguments, or other raw session payloads. It ships only bounded metric names and normalized attribute values.

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
| Linux systemd | `telemetron install` supported | `telemetron start` supported | `telemetron status` supported |
| macOS | daemon install unsupported | foreground `start` supported | `status` supported |
| Other | daemon install unsupported | best-effort foreground `start` | limited `status` detail |

## Security

`telemetron` is designed so message content never leaves the box. The collector emits only allowlisted metric names with a bounded attribute set, reads its bearer token from a `0400` file, and requires HTTPS by default. Plaintext endpoints are rejected unless `insecure_endpoint` is explicitly enabled for local testing.

## Disabling telemetry

`telemetron` honors its own opt-out signals **and** the [`lowkey`](https://github.com/inceptionstack/lowkey) installer-family signals, so a single opt-out disables both tools when they are deployed together:

**Shared**
- `DO_NOT_TRACK=1` — [consoledonottrack.com](https://consoledonottrack.com) community standard. Truthy: `1|true|yes|on` (case-insensitive).

**telemetron-specific**
- `TELEMETRON_TELEMETRY=0` — falsy values `0|false|no|off` opt out; unset or any other value keeps telemetry enabled.
- `~/.telemetron/telemetry-off` — marker file under the service user’s home.

**Lowkey-family (inherited when deployed via `lowkey`)**
- `LOWKEY_TELEMETRY=0`
- `~/.lowkey/telemetry-off`

When any signal is present, `telemetron start` exits cleanly without loading config, reading the token, or opening any sockets. `telemetron status` reports `telemetry: disabled (<source>)` instead of probing the service.

Note: environment variables set in the interactive shell that ran the `lowkey` installer do **not** propagate into the `telemetron` systemd unit. For `lowkey`-installed deployments, `lowkey` should drop the marker file into the `telemetron` service user’s home so the opt-out sticks across restarts.

## Status

`alpha` for `v0.2`.

## Reporting issues

- **Bugs / feature requests:** [open a GitHub issue](https://github.com/inceptionstack/telemetron/issues/new/choose) using one of the templates.
- **Security vulnerabilities:** see [SECURITY.md](SECURITY.md) — do **not** file public issues for anything involving token exposure or telemetry content leakage.

## License

Apache-2.0. See [LICENSE](LICENSE).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Pre-commit hooks

The repo ships a [`.pre-commit-config.yaml`](.pre-commit-config.yaml) that runs `git-secrets`, `gofmt`, `go vet`, and whitespace hygiene on every commit. Enable it locally:

```bash
# one-time: install the pre-commit framework (https://pre-commit.com)
pipx install pre-commit        # or: brew install pre-commit

# one-time: configure git-secrets for this clone
#   - registers AWS + telemetron token patterns
#   - installs the git-secrets pre-commit / commit-msg / prepare-commit-msg hooks
bash scripts/git-secrets-install.sh

# enable the pre-commit hooks for this clone
pre-commit install

# optional: run across the whole tree once
pre-commit run --all-files
```

The same `git-secrets` scan runs in CI via [`.github/workflows/ci.yml`](.github/workflows/ci.yml) (`secrets-scan` job, full working tree **and** history). Local hooks + CI keep the token patterns (`lpk_live_*`, `ghp_*`, `github_pat_*`, `x-access-token:*`, AWS keys, …) out of commits.
