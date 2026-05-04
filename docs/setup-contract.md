# telemetron setup — stable contract

This document is the stable reference for third-party installers that
invoke `telemetron setup`. Most humans should use `telemetron setup
--help` instead; this file exists so the Loki bundle installer (and any
future bundled installer) has a versioned, machine-readable contract to
build against.

**Schema version:** `telemetron.setup.v1`

## Invocation

```bash
telemetron setup [flags]
```

All flags, environment variables, and resolution rules are listed in
`telemetron setup --help` and [configuration.md](configuration.md).
This file focuses on the parts that are guaranteed to remain stable.

## Non-interactive contract

`telemetron setup --non-interactive --json` never prompts, never hangs,
never silently falls back to empty defaults. It exits non-zero as soon as
any required input cannot be resolved from flags, environment, detection,
or existing state.

Required inputs:

- `endpoint` — from `--endpoint` or `$TELEMETRON_ENDPOINT`. If the command
  is reconciling an already-installed `telemetron` and neither source is
  provided, the existing `/etc/telemetron/config.yaml` value is reused.
- `token` — from `--token-file`, `$TELEMETRON_TOKEN_FILE`,
  `$TELEMETRON_TOKEN`, an existing `/etc/telemetron/token`, or
  anonymous auto-enrollment (when no other token source is configured
  and `TELEMETRON_NO_AUTO_ENROLL` is not set).

Inferred inputs (never a missing-field error in non-interactive mode):

- `mode`, `session-dir`, `run-as`, `agent-name` — from
  `$HOME/.openclaw/agents/*/sessions` detection. Ambiguity (multiple
  agent slots and no `main`) is a fatal `ambiguous_agent` error; pass
  `--session-dir` to disambiguate.
- `deployment-id` — default `loki@<hostname>` (or
  `loki-<agent>@<hostname>` when detected agent is not `main`).
- `tier` — heuristic (`development` for interactive sudo, `production`
  otherwise). Overridable via `--tier`.

## JSON event stream

When `--json` is set, each lifecycle phase emits exactly one line of JSON
on stdout. Every event carries `"schema": "telemetron.setup.v1"` and a
stable `"event"` name. Installers must tolerate additive fields.

Event sequence on a successful first install:

1. `agent.detected` — when a detector matched.
2. `config.resolved` — final resolved state (endpoint, mode, session_dir,
   deployment_id, tier, run_as).
3. `token.loaded` — token resolved; includes `source`
   (`token-file|env|existing|auto-enroll`). The token value is never logged.
4. `token.written` — token file materialised at its final path.
5. `service.installed` — systemd unit file written.
6. `service.started` — `systemctl enable --now` succeeded.
7. `healthcheck.passed` — first flush observed in the status store.
8. `setup.completed` — final envelope. Includes
   `action_taken: installed|updated|unchanged|dry_run` and
   `health: passed|skipped`.

Idempotent rerun (no changes) short-circuits after phase 2 with
`action_taken: unchanged`. A rerun that only rotates the token keeps the
same sequence and reports `action_taken: updated`.

## Failure envelope

On any failure, the final line is `setup.failed` with:

- `error_code` — see list below.
- `missing_fields` — present only for `missing_required_input`.
- `hint` — short string naming the accepted flag/env sources.
- `error` — human-readable detail for operators. Never a reliable
  machine-readable contract beyond the `error_code`.

Stable error codes:

- `missing_required_input`
- `ambiguous_agent`
- `token_read_failed`
- `systemd_install_failed`
- `service_start_failed`
- `health_check_failed`
- `precondition_failed` — non-Linux host, non-root invocation, etc.
- `detection_failed` — detector itself errored (rare).
- `invalid_config` — bootstrap config validation failed.

## Compatibility policy

- New events may be added in any version.
- New fields may be added to existing events in any version.
- Existing event names, existing field names, and existing error codes
  are stable until the next major schema bump (`telemetron.setup.v2`).
- Installers must skip unknown events and unknown fields.

## Example: bundled installer snippet

```bash
export TELEMETRON_ENDPOINT="https://telemetry.example.com/v1/metrics"
export TELEMETRON_TOKEN_FILE="/run/secrets/telemetron-token"

telemetron setup --non-interactive --json --yes \
  --deployment-id "$(hostname)" \
  --tier production \
  | while read -r line; do
      event=$(jq -r '.event' <<<"$line")
      case "$event" in
        setup.completed) echo "telemetron ok: $(jq -r '.action_taken' <<<"$line")" ;;
        setup.failed)    jq -c '{error_code, hint, missing_fields}' <<<"$line" ; exit 1 ;;
      esac
    done
```
