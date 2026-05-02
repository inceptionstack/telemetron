# lokiotel

`lokiotel` is a static Go sidecar that tails local Loki/OpenClaw session state, derives bounded metrics, and exports them to an OTLP/HTTP metrics endpoint with bearer auth.

## Quickstart

```bash
make test
make build
sudo ./lokiotel install \
  --endpoint https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/metrics \
  --token "$LOKIOTEL_TOKEN" \
  --mode openclaw \
  --session-dir "$HOME/.openclaw/agents/main/sessions" \
  --deployment-id mvp-002 \
  --tier internal
sudo systemctl status lokiotel
./lokiotel status
```

Linux supports full `install`/`uninstall` service management. On macOS, `lokiotel start --config ~/.config/lokiotel/config.yaml` is supported for local runs, while daemon install remains unsupported.

## Configuration

`/etc/lokiotel/config.yaml`

```yaml
mode: openclaw
endpoint: https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/metrics
token_file: /etc/lokiotel/token
insecure_endpoint: false

openclaw:
  session_dir: /home/ec2-user/.openclaw/agents/main/sessions
  flush_interval: 15s
  scan_interval: 15s
  state_file: /var/lib/lokiotel/openclaw.state.json

declared:
  deployment_id: mvp-002
  tier: internal
  environment: development
  pack_version: lokiotel-0.1
```

Env overrides:

```bash
export LOKIOTEL_CONFIG=/etc/lokiotel/config.yaml
export LOKIOTEL_ENDPOINT=https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/metrics
export LOKIOTEL_TOKEN_FILE=/etc/lokiotel/token
export LOKIOTEL_MODE=openclaw
export LOKIOTEL_LOG_LEVEL=info
```

`LOKIOTEL_TOKEN` is supported as an inline fallback when `token_file` is missing or unreadable. The bearer token is never stored inline in YAML.

`--insecure-endpoint` / `insecure_endpoint: true` is only for local testing against a plaintext OTLP endpoint. Production installs should use `https://` and leave this disabled.

## Commands

```bash
lokiotel install
lokiotel uninstall
lokiotel start
lokiotel status
lokiotel version
```

`lokiotel status` exits non-zero when the service is not active.

## Adding a collector

Adding a new mode is intentionally additive:

1. Create `internal/<mode>/` and implement `collectorapi.Collector`.
2. Add an `init()` that calls `collectorapi.Register(...)` with the mode name, config decoder, factory, and defaults.
3. Keep that mode’s config schema and validation inside its own package.
4. Add one blank import in `cmd/lokiotel/main.go`, or a build-tagged blank import file if the mode is optional.
5. Run `go test ./...` and, if the mode is optional, `go test -tags <tag> ./...`.

`internal/demo/` is a minimal build-tagged example. Build it with `go build -tags demo ./cmd/lokiotel`.

## Build and test

```bash
make test
make lint
go build -trimpath -ldflags="-s -w" ./cmd/lokiotel
GOOS=darwin GOARCH=arm64 go build -trimpath ./cmd/lokiotel
```

## Troubleshooting

- `token file permissions`: `/etc/lokiotel/token` should be `0600`.
- `401/403 exports`: verify the token source and endpoint; auth failures back off to `flush_interval * 6`.
- `no metrics emitted`: confirm `openclaw.session_dir` exists and contains complete `.jsonl` lines.
- `status shows stale heartbeat`: inspect `journalctl -u lokiotel` for flush logs and exporter failures.
- `systemd install failed`: run as `root` or under `sudo`, and ensure `systemctl` and `useradd` are available.

## Open questions

1. The spec says out-of-set enum values must normalize to `unknown`, but the listed `outcome` enum omits `unknown`. The implementation normalizes invalid outcomes to `unknown`; confirm the server accepts that value.
2. Resource attributes are included only for local debugging and should be treated as advisory because the server strips caller-supplied resource identity.
