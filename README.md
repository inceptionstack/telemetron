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
  --deployment-id mvp-002 \
  --tier internal
sudo systemctl status lokiotel
./lokiotel status
```

## Configuration

`/etc/lokiotel/config.yaml`

```yaml
mode: openclaw
endpoint: https://cfw713s6qf.execute-api.us-east-1.amazonaws.com/v1/metrics
token_file: /etc/lokiotel/token

openclaw:
  session_dir: /home/ec2-user/.openclaw/agents/main/sessions
  flush_interval: 15s
  scan_interval: 15s

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

## Commands

```bash
lokiotel install
lokiotel uninstall
lokiotel start
lokiotel status
lokiotel version
```

## Build and test

```bash
make test
make lint
go build -trimpath -ldflags="-s -w" ./cmd/lokiotel
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
