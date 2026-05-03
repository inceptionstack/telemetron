# Grafana dashboards

## `telemetron-openclaw.json`

Live dashboard for the OpenClaw telemetron sidecar.

Anchors on metrics that telemetron actively emits:
- `pack_agent_turn_total` — rate of agent turns
- `pack_tool_call_total` — tool invocations, broken out by `tool_class`
- `pack_emitter_heartbeat_total` — sidecar liveness

### Import

1. In Grafana, open **Dashboards → New → Import**
2. Paste the JSON from `telemetron-openclaw.json`
3. Select your Amazon Managed Prometheus data source
4. Click **Import**

The JSON ships with a placeholder datasource UID (`PROMETHEUS_DS_UID`); Grafana
will prompt you to map it to the correct data source on import.

### What's not here (yet)

The previous MVP dashboard anchored on `pack_session_start_total`, which
telemetron still emits but only on first-sight of a session file. After a
sidecar restart, cumulative counters are reset and new `rate()` windows can
read as zero until a new session starts. Use the heartbeat-derived "Active
deployments" panel as your go-to liveness signal.
