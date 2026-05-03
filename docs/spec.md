# clawtello — OTLP metrics sidecar for stateful agents

## Purpose

A single small Go binary `clawtello` that tails local session state from
agent installation on the same host, reads that agent's local state, converts
it to OTLP metrics, and ships them to a central OTLP ingester (today: our
AWS API GW + Lambda ingester at
`https://your-otlp-gateway.example.com/v1/metrics`).

Think of it as a generalised OTLP sidecar for any stateful agent that writes
a session transcript to disk. One static Go binary (no venv, no pip),
* config-driven "collection mode" so we can add more Loki flavours later
  without forking the emitter
* daemon install/uninstall lifecycle built into the binary

## CLI shape

```
clawtello install          # install as a systemd unit and start it
clawtello uninstall        # stop + remove the systemd unit
clawtello start            # foreground run (used by the systemd unit)
clawtello status           # show unit state + last heartbeat + last export
clawtello help | --help    # usage
clawtello version          # build metadata
```

Global flags:

```
--config <path>           # default /etc/clawtello/config.yaml
--log-level <level>       # trace|debug|info|warn|error (default info)
```

`install` should accept overrides that are written into the config file if
supplied on the CLI:

```
--endpoint <url>          # OTEL_EXPORTER_OTLP_ENDPOINT
--token   <bearer>        # written to <token_file> (mode 0400)
--mode    <collection>    # "openclaw" (v1 only)
--deployment-id <id>
--tier    <internal|production|development|staging|unknown>
```

If a value isn't supplied, fall back to env var (documented below), then to
existing config file, then fail loudly with a helpful message.

## Config file

`/etc/clawtello/config.yaml` (created by `install`, editable by operator):

```yaml
# Collection style. v1 only supports "openclaw".
mode: openclaw

# OTLP/HTTP endpoint the agent should POST to.
endpoint: https://your-otlp-gateway.example.com/v1/metrics

# Path to a file containing the bearer token. Must be chmod 0600.
# Never store the token inline in the YAML.
token_file: /etc/clawtello/token

# Per-mode settings. Unknown keys for the selected mode must be rejected.
openclaw:
  # Directory the openclaw emitter watches for session jsonl files.
  session_dir: $HOME/.openclaw/agents/main/sessions
  # How often to flush metric batches to the endpoint.
  flush_interval: 15s
  # How often to re-scan the session_dir for new sessions.
  scan_interval: 15s

# Global metadata carried on every request (not label values).
# The server overrides these from the token, but they're useful for
# local debugging / observability.
declared:
  deployment_id: mvp-002
  tier: internal
  environment: development
  pack_version: clawtello-0.1
```

Env-var overrides (these win over config file):

```
CLAWTELLO_CONFIG              # override --config
CLAWTELLO_ENDPOINT
CLAWTELLO_TOKEN               # inline token (fallback if token_file missing)
CLAWTELLO_TOKEN_FILE
CLAWTELLO_MODE
CLAWTELLO_LOG_LEVEL
```

## Collection modes

The internal interface every mode must implement:

```go
type Collector interface {
    // Name returns the mode string ("openclaw", ...).
    Name() string
    // Start begins collecting. It runs until ctx is done.
    // Emit metrics by calling sink.Counter(...) / sink.Heartbeat() etc.
    Start(ctx context.Context, sink MetricSink) error
}
```

### mode: openclaw (v1)

Port of the existing `loki-pack-emitter.py` behaviour:

1. Every `scan_interval`, list `*.jsonl` files under `session_dir`.
2. For each file, remember the last byte offset processed in a durable
   state file (`/var/lib/clawtello/openclaw.state.json`). New files start
   at offset 0. Reuse-after-rotate: if file size < last offset, reset to 0.
3. Tail the new bytes, decode each line as JSON, and emit:
   * `pack.session.start` once per new session (first time a session file
     is seen). Attributes: `outcome=success`, `model.family=<derived>`,
     `session.type=<derived from path or file content>`.
   * `pack.agent.turn` per top-level assistant-or-user role flip.
     Attributes: `outcome`, `model.family`.
   * `pack.tool.call` per `toolCall` block. Attributes: `outcome`,
     `tool.class=<derived from tool name>`.
   * `pack.error` per error event seen in the transcript. Attributes:
     `error.type`.
4. Every `flush_interval` emit one `pack.emitter.heartbeat` counter++ and
   flush the OTLP batch.

Derivation helpers should live in `internal/openclaw/derive.go` and be
unit-tested (model.family, tool.class, session.type, error.type).

### Future modes

Other modes (`claude-code-sidekick`, `goose`, …) are out of scope for v1,
but the code structure must make adding one just a matter of dropping a new
package under `internal/<mode>/` that implements `Collector` and registering
it in `internal/collectors/registry.go`. No switch statements sprinkled
across the codebase.

## Wire contract (identical to current emitter)

* Transport: OTLP/HTTP (`POST /v1/metrics`), `Content-Type: application/x-protobuf`.
* Auth: `Authorization: Bearer <token>` read from `token_file`.
* The server strips all caller-supplied resource attributes and injects
  its own identity from the token, so this binary MUST NOT rely on
  `service.name` / `host.name` / `pack.name` being kept. For local
  debugging we may set them, but tests must not assert they round-trip.
* Allowed metric names (anything else will be dropped server-side):
    `pack.session.start`, `pack.agent.turn`, `pack.tool.call`,
    `pack.error`, `pack.emitter.heartbeat`.
* Allowed attribute keys per metric (see `internal/contract/allowlist.go`):
    session.start: `outcome`, `model.family`, `session.type`
    agent.turn:    `outcome`, `model.family`
    tool.call:     `outcome`, `tool.class`
    error:         `error.type`
    heartbeat:     (none)
* Enum values (values outside the set must be normalised to `unknown`):
    outcome:       `success|error|aborted|timeout`
    model.family:  `anthropic|openai|bedrock|gemini|openclaw|unknown`
    tool.class:    `shell|file|http|system|aws|message|search|memory|agent|other`
    error.type:    `transient|permanent|config|auth|quota|prompt|unknown`
    session.type:  `main|heartbeat|cron|subagent|ephemeral|unknown`

These constants must live in one Go package (`internal/contract/`) so
both the collector and the OTLP exporter see identical source of truth.
Bonus: add a `go test` that walks every `Collector`'s metric output through
the allowlist and fails if anything would be dropped — the binary should
never ship unsanitary metrics.

## Daemon install

`clawtello install`:

1. Verify we're root (or running under sudo); bail with clean error otherwise.
2. Copy the running binary to `/usr/local/bin/clawtello` (skip if already
   identical).
3. Create `/etc/clawtello/` (0755) and write `config.yaml` if missing.
4. Write the bearer token to `/etc/clawtello/token` (0400, owner `clawtello`
   if that user exists, else `root`).
5. Install the systemd unit at `/etc/systemd/system/clawtello.service`:

```ini
[Unit]
Description=clawtello OTLP metrics sidecar
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=clawtello
Group=clawtello
ExecStart=/usr/local/bin/clawtello start --config /etc/clawtello/config.yaml
Restart=on-failure
RestartSec=10
# Don't leak secrets to journal.
LimitCORE=0
ProtectSystem=strict
ProtectHome=read-only
PrivateTmp=true
NoNewPrivileges=true
ReadWritePaths=/var/lib/clawtello
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
```

6. Create the `clawtello` system user (no shell, home `/var/lib/clawtello`)
   if it doesn't exist.
7. `systemctl daemon-reload && systemctl enable --now clawtello.service`.
8. Print a one-line summary: endpoint, mode, deployment_id, unit status.

`clawtello uninstall` reverses 5/7 and leaves the config + state files in
place (idempotent, safe to re-run).

## Status command

`clawtello status` should print (without hitting the network):

```
unit:          active (running) since 2026-05-02 22:10 UTC
mode:          openclaw
endpoint:      https://your-otlp-gateway.example.com/v1/metrics
deployment_id: mvp-002
last flush:    2026-05-02 22:11:15 UTC (3s ago, 4 metrics)
last heartbeat:2026-05-02 22:11:00 UTC (18s ago)
session_dir:   $HOME/.openclaw/agents/main/sessions (103 files)
state file:    /var/lib/clawtello/openclaw.state.json (32 sessions tracked)
```

Heartbeat / flush timestamps live in a tiny status file the main process
writes on every tick. No IPC, just read-the-file.

## Observability + failure modes

* Every flush must log one structured line (JSON) at info:
  `{"event":"flush","mode":"openclaw","batch_metrics":4,"bytes":2150,"http_status":200,"took_ms":37}`
* HTTP failures should log at warn with the status + short body, then the
  batch is **dropped** (we don't buffer; the MVP emitter didn't either).
  Add a counter for dropped batches and expose it in `status`.
* Authorisation failures (401/403) should log at error and back off to
  a `flush_interval * 6` retry. We want noisy failure, not storm.
* Graceful shutdown on SIGTERM: flush in-flight batch, save state, exit 0
  within 5s.

## Repo layout

```
/
├── cmd/clawtello/main.go              # cobra root
├── cmd/clawtello/install.go
├── cmd/clawtello/uninstall.go
├── cmd/clawtello/start.go
├── cmd/clawtello/status.go
├── internal/config/config.go         # YAML + env merge
├── internal/contract/allowlist.go    # metric names, attr keys, enums
├── internal/contract/normalize.go    # enum guarding
├── internal/collectors/registry.go   # mode -> constructor
├── internal/openclaw/collector.go    # Collector impl
├── internal/openclaw/derive.go       # tool.class / model.family / …
├── internal/openclaw/state.go        # durable offsets
├── internal/otlp/sink.go             # buffered MetricSink impl
├── internal/otlp/exporter.go         # POST /v1/metrics via protobuf
├── internal/systemd/unit.go          # install/uninstall
├── internal/status/store.go          # status file read/write
├── go.mod  go.sum
├── Makefile
├── README.md
├── CHANGELOG.md
└── .github/workflows/ci.yml          # go test + golangci-lint
```

## Tooling / quality gates

* Go 1.22+, `go.mod` with `toolchain` pin.
* `go vet ./... && go test ./... && golangci-lint run` must pass.
* Use `github.com/spf13/cobra` for commands, `github.com/spf13/viper` for
  config, `google.golang.org/protobuf` + OpenTelemetry proto types for OTLP.
  Keep total direct deps below ~10.
* All tests must run without network. Exporter tests stub an httptest
  server; collector tests run against a temp `session_dir`.
* Makefile targets: `build`, `test`, `lint`, `run`, `release`. `release`
  uses `go build -trimpath -ldflags='-s -w'` for a tiny arm64 + amd64
  static binary.
* GitHub Actions CI: `go test`, `golangci-lint`, and a `goreleaser` dry-run
  on every PR. On tag push, publish release binaries.
* **npm supply-chain-style quarantine does not apply** (this is Go), but
  the Makefile MUST set `GOFLAGS=-mod=readonly` and the CI MUST verify
  `go.sum` is clean (`go mod verify`).

## What done looks like (ship checklist)

- [ ] `go build ./cmd/clawtello` produces a static arm64+amd64 binary.
- [ ] `clawtello install --endpoint ... --token ... --mode openclaw
        --deployment-id mvp-002 --tier internal` on a fresh EC2 installs a
  working systemd unit within 5s.
- [ ] `journalctl -u clawtello` shows heartbeat flushes every 15s, HTTP 200.
- [ ] Central Grafana `Heartbeat rate` panel shows a new line for
  `deployment_id=mvp-002`.
- [ ] `clawtello status` prints unit + endpoint + last flush timestamps.
- [ ] `clawtello uninstall` cleanly removes the unit; re-running is a no-op.
- [ ] CI is green on first PR.

## Non-goals

* Traces, logs, profiles — metrics only for v1.
* Sampling, tail-based buffering — dropped-on-error is fine.
* Windows / macOS service lifecycle — Linux systemd only.
* Packaging (rpm/deb) — release tarball is enough for v1.

## Open questions for the implementer

Resolve these in the README before merging v1:

1. How is `model.family` derivable from an openclaw session file?
   (Collectors are expected to map their native session format to a small
   bounded enum of model families; contributors should document their
   mapping in `docs/configuration.md`.)
2. Do we want TLS pinning on the exporter? (MVP: no, just cert validation.)
3. Where should `clawtello status` store its cache? (Proposed:
   `/var/lib/clawtello/status.json`.)
