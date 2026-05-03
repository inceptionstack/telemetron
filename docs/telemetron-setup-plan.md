# telemetron setup — simplification plan

Status: **draft / pre-implementation**
Scope: onboarding UX for `telemetron` when attached to a fresh Loki install
or any other OTLP-consuming bundled installer.
Authors: Codex brainstorm + debate (2026-05-03 rounds 1 + 2), distilled by
Loki@FastStart.

## Why this exists

Today `telemetron install` exposes 7 inputs (endpoint, token, mode,
session-dir, deployment-id, tier, run-as) and forces the user to pre-stage
a bearer token in a root-owned file before invoking it. Most of those
inputs are identical for every Loki install, and the token plumbing is
easy to get wrong.

The goal: **zero-friction bundled install** for the Loki installer (the
dominant caller), with a clean fallback for solo humans attaching
`telemetron` to an already-running agent on their laptop. No Loki
branding, no loss of the cross-agent positioning.

## Ship-this summary

1. Add a new subcommand: `telemetron setup`.
2. It is **non-interactive-first**. Interactive behavior exists only to
   fill missing required inputs when stdin is a TTY and `--non-interactive`
   was not requested.
3. It auto-detects Loki/OpenClaw and fills `mode`, `session-dir`,
   `run-as`, `tier`, and default `deployment-id`.
4. It prompts only for unresolved required inputs: `endpoint`, `token`,
   and optionally `deployment-id` if no default is accepted. Prompts are
   fallbacks, not a wizard.
5. Non-interactive failures emit a machine-readable JSON error envelope in
   `--json` mode; human-readable stderr otherwise. Required inputs
   missing → fail fast, never silently fall back.
6. `setup` becomes the documented path for installers, CI, and humans;
   `install` remains the lower-level primitive but is no longer the
   primary story.
7. The `--token` flag is deprecated — it leaks via `ps` and shell history.

## Canonical flow and fallback flow

### Flow A — canonical bundled install from the Loki installer / CI / image bake

```bash
# Inside the Loki installer, running as root on a fresh VPS or laptop:
#   1. Provision the Loki service.
#   2. Mint a telemetron bearer token from FastStart's provisioning API.
#   3. Hand both to telemetron setup:

TELEMETRON_ENDPOINT="https://telemetry.example.com/v1/metrics" \
TELEMETRON_TOKEN_FILE=/run/secrets/telemetron-token \
telemetron setup --yes --non-interactive --json
```

Or, equivalently, piped from an outer orchestrator:

```bash
telemetron setup --yes --non-interactive --json \
  --endpoint https://telemetry.example.com/v1/metrics \
  --token-file /run/secrets/telemetron-token \
  --deployment-id "${HOSTNAME}" \
  --tier production
```

Expected behavior:

- No prompts, ever. If required explicit inputs (endpoint, token) are
  missing, or required inferred inputs (mode, session-dir) cannot be
  resolved deterministically, exit non-zero with a specific error listing
  what was missing and where it could have come from.
- Same defaults as the interactive fallback: auto-detect Loki session dir,
  hostname-based deployment id, `run-as` = `$SUDO_USER` or explicit
  `--run-as`.
- `--yes` skips the confirmation prompt; the summary is **always printed**
  (for logs and audit). Without `--yes`, on non-TTY stdin, no prompt is
  issued either — the summary is printed and the command proceeds.
- Structured output is a first-class contract: `--json` emits
  line-delimited JSON events per phase plus a final success/failure
  envelope.

### Flow B — fallback interactive install (solo developer, already-running Loki)

```bash
curl -fsSL https://raw.githubusercontent.com/inceptionstack/telemetron/main/install.sh | sh
sudo telemetron setup
```

Expected experience:

```
Detected Loki/OpenClaw for user roy at /home/roy/.openclaw/agents/main/sessions
OTLP/HTTP endpoint: https://telemetry.example.com/v1/metrics
Bearer token (hidden): ********

About to install telemetron as a systemd service:
  user:         roy
  mode:         openclaw
  session dir:  /home/roy/.openclaw/agents/main/sessions
  deployment:   loki@roy-laptop
  tier:         development
  endpoint:     https://telemetry.example.com/v1/metrics
  token file:   /etc/telemetron/token (0400, owned by roy)

Proceed? [Y/n]: y

systemd: telemetron.service enabled + started
first flush in 15s... ok (http 200)
```

The prompts are fallbacks for unresolved required inputs, not a scripted
wizard. Deployment-id is usually *not* prompted because
`loki@<hostname>` is a deterministic default; the summary shows it and
the user can rerun with `--deployment-id` to override.

## Command surface

### `telemetron setup` (new)

Flags/env/detection/existing-state resolve desired state first; prompts
are used only for unresolved required inputs in interactive mode.

| Flag | Purpose |
|---|---|
| `--endpoint` | OTLP/HTTP endpoint |
| `--token-file` | path to an existing token file (read once, installed into place) |
| `--mode` | override auto-detection (`openclaw`, `demo`, …) |
| `--session-dir` | override auto-detection |
| `--run-as` | override `$SUDO_USER` |
| `--deployment-id` | override the default `loki@<hostname>` |
| `--tier` | override the auto-detected tier |
| `--yes` | skip the "proceed?" confirmation prompt (summary is always printed) |
| `--non-interactive` | never prompt; fail fast on missing required input |
| `--json` | emit line-delimited JSON events and final status envelopes on stdout; **this is a supported installer contract, not debug output** |
| `--dry-run` | print what would happen, touch nothing |

No `--reconfigure` flag. `setup` is always safe to rerun and reconciles
existing installs to the requested state.

Env vars (same precedence as the CLI flag of the same name):

- `TELEMETRON_ENDPOINT`
- `TELEMETRON_TOKEN`        (bare token; CI only — not advertised to humans)
- `TELEMETRON_TOKEN_FILE`
- `TELEMETRON_MODE`
- `TELEMETRON_SESSION_DIR`
- `TELEMETRON_RUN_AS`
- `TELEMETRON_DEPLOYMENT_ID`
- `TELEMETRON_TIER`

### `telemetron install` (existing, unchanged)

Stays as the low-level primitive. Remains fully scriptable. Docs demote it
to a "CI / power user" section but do not remove it.

### `--token` (existing, deprecated)

Keeps working for one minor version, emits a loud warning:

```
DEPRECATED: --token leaks via shell history and /proc/<pid>/cmdline.
Use --token-file, TELEMETRON_TOKEN, or an interactive prompt instead.
```

## Auto-detection logic

Detection is part of default resolution for both bundled and human
invocations; when ambiguous, interactive may ask and non-interactive
**must fail with a deterministic error**.

Run in this order:

1. If `--mode` is passed explicitly, **skip detection entirely**. Respect
   the user's choice.
2. Resolve the target user (`--run-as` → `$SUDO_USER` → current user).
3. Look for `/home/<user>/.openclaw/agents/*/sessions`.
   - If exactly one: use it.
   - If `main` exists: prefer `main`.
   - If multiple and no `main`:
     - interactive: prompt "Which agent? [a/b/c]" (one extra prompt).
     - non-interactive: error out with the list and the `--session-dir`
       hint.
4. Optionally inspect `/etc/systemd/system/loki-agent.service` for
   stronger confidence / a nicer "detected as Loki v1.2.3" message. Not
   required.
5. Set:
   - `mode = openclaw`
   - `session-dir` = discovered path
   - `run-as` = target user
   - `deployment-id` = `loki@<hostname>` (or `loki-<agent>@<hostname>` if
     not `main`)
   - `tier` = heuristic (see below)

Do **not** use `pgrep loki` for detection. Too brittle, too easy to
false-match.

### Tier heuristic

- `--tier` or `TELEMETRON_TIER` always wins.
- Else: if `$SUDO_USER` is a non-system user (uid ≥ 1000) AND stdin is a
  TTY → `development`.
- Else: if `/sys/class/dmi/id/chassis_type` indicates rack/blade/VM, or
  hostname matches common cloud patterns → `production`.
- Else: `development`.

We intentionally drop `internal|staging|unknown` from the onboarding flow.
They remain valid values via `--tier` for advanced use.

## Required input resolution

### Endpoint resolution

1. `--endpoint <url>`
2. `TELEMETRON_ENDPOINT` env var
3. Existing installed config (`/etc/telemetron/config.yaml`) if
   reconciling an already-installed setup with no new endpoint supplied.
4. Interactive fallback prompt on TTY.
5. Else: fail with `missing_required_input` + `endpoint`.

### Token resolution

1. `--token-file <path>`
2. `TELEMETRON_TOKEN_FILE` env var
3. `TELEMETRON_TOKEN` env var (bare; CI only)
4. Interactive hidden prompt on TTY.
5. On rerun, existing installed token may be reused only when no new
   token source is supplied and the command is reconciling an
   already-installed setup.
6. Else: fail with `missing_required_input` + `token`.

`TELEMETRON_TOKEN` is consumed once, never logged, never written to the
systemd unit environment, never passed to child processes, and should be
scrubbed from the current process environment after read. The token is
written to `/etc/telemetron/token`, mode `0400`, owned by the run-as
user.

### Deployment-id resolution

1. `--deployment-id <id>`
2. `TELEMETRON_DEPLOYMENT_ID` env var
3. Auto-default `loki@<hostname>` (or `loki-<agent>@<hostname>` if the
   detected agent is not `main`).
4. In interactive mode, the default is used without prompting. The user
   can override via flag or env var. Only if detection fails AND no
   default is derivable do we prompt.

### Detection-derived fields

`mode`, `session-dir`, `run-as`, and `tier` resolve via the auto-detection
logic above. In non-interactive mode, ambiguous detection is a fatal
error with a deterministic exit code and a machine-readable error
envelope listing the ambiguous candidates.

## Resolution model

| Input | Source order | Default / inference | Interactive fallback? | Error in non-interactive? |
|---|---|---|---|---|
| `endpoint` | flag → env → existing-config | — | hidden prompt | **yes, `missing_required_input`** |
| `token` | flag → env → existing-token (on rerun) | — | hidden prompt | **yes, `missing_required_input`** |
| `deployment-id` | flag → env → auto-default | `loki@<hostname>` | only if default underivable | no (default used) |
| `mode` | flag → env → detection | `openclaw` if detected | ambiguous-agent prompt | **yes, `ambiguous_agent`** |
| `session-dir` | flag → env → detection | discovered path | no (follows mode) | **yes, if detection failed** |
| `run-as` | flag → `$SUDO_USER` → current user | `$SUDO_USER` | no | no |
| `tier` | flag → env → heuristic | `development`/`production` | no | no |

## Idempotence contract

- `setup` is always rerunnable.
- Re-running with same resolved state is a **no-op success** (exit 0,
  `action_taken: unchanged`).
- Re-running with changed endpoint/token/deployment updates on-disk state
  and restarts/reloads the service as needed
  (`action_taken: updated`).
- First-time installation is `action_taken: installed`.
- No separate `--reconfigure` mode exists.

## Success contract

- Exit 0 only after service install/update **and** post-start health
  verification succeed. "Installed but not yet flushing" is not success.
- In `--json` mode, emit a final `setup.completed` event with fields:
  `endpoint`, `mode`, `session_dir`, `deployment_id`, `tier`, `run_as`,
  `token_path`, `action_taken` (`installed|updated|unchanged`),
  `health` (`passed`).
- In text mode, the last stdout line prints whether the run installed,
  updated, or made no changes.

## Failure contract

- Non-zero exit for validation, detection, install, start, or
  health-check failures.
- Stable `error_code` values in `--json` mode. Minimum set:
  `missing_required_input`, `ambiguous_agent`, `token_read_failed`,
  `systemd_install_failed`, `service_start_failed`,
  `health_check_failed`, `precondition_failed`.
- `missing_fields` array listing every unresolved required input.
- `hint` field naming the accepted flag / env var sources.
- Example envelope:
  ```json
  {
    "event": "setup.failed",
    "error_code": "missing_required_input",
    "missing_fields": ["endpoint"],
    "hint": "set --endpoint or TELEMETRON_ENDPOINT"
  }
  ```

## JSON event schema

The minimum stable event set for `--json` mode:

- `config.resolved` — emitted after flag/env/detection merge
- `agent.detected` — emitted when an agent detector matched
- `token.loaded` — token source resolved (source identified, value not logged)
- `token.written` — token file materialised at destination
- `service.installed` — systemd unit file written
- `service.started` — `systemctl enable --now` succeeded
- `healthcheck.passed` — first flush verified
- `setup.completed` — final success envelope
- `setup.failed` — final failure envelope

Event schema versioning: include `"schema": "telemetron.setup.v1"` on
every event so the Loki installer can assert compatibility.

## The Loki bundle contract (why non-interactive-first matters)

Telemetron will ship bundled with the Loki installer. That means:

- Loki's `curl | sh` bootstrap must be able to invoke `telemetron setup`
  with zero prompts, zero hangs, zero ambiguous defaults.
- The Loki installer already knows the endpoint (FastStart internal OTLP)
  and can mint a bearer token from its own provisioning flow. It passes
  both as env vars or a token file.
- **Interactive mode must be the same reconciliation code path as
  non-interactive**, with prompts only at unresolved-input boundaries.
  No divergent logic, no parallel "wizard" flow.
- Failures must emit human-readable stderr in text mode and a
  machine-readable error envelope in `--json` mode.
- `--json` emits one structured event per phase plus a final
  success/failure envelope, so the Loki installer can surface setup
  progress to the user natively without parsing free-text output.

## What we are NOT doing

- No `telemetron loki-install` command. Loki support is a *detector*, not
  a product identity.
- No wizard-first control flow. `setup` is a reconciler with optional
  prompts, not a questionnaire.
- No removing or renaming `telemetron install`. CI users who already
  script against it stay working.
- No auto-running `setup` from `install.sh | sh` without consent. The
  installer drops the binary and prints the next-step command. A
  one-shot "curl | sh installs a systemd service" is too aggressive on
  first launch. (We can revisit once the UX has soaked.)
- No auto-detection of endpoint/token. Those are project-scoped secrets
  that must be explicit. Auto-detecting them would be a security mistake.
- No `--reconfigure` flag. `setup` is always safe to rerun.

## Red flags to avoid

- Documenting `--token` anywhere in README. Already a bug in current
  docs.
- Asking Loki users to pick `mode` or `session-dir`. Implementation
  detail.
- Using `pgrep` for detection. Brittle and easy to false-match.
- Branding the command surface around Loki. Keeps future agents (Codex,
  Claude Code) plug-compatible.
- A silent `setup` that succeeds when one of
  `endpoint/token/session-dir` fell back to an empty default. Every
  fallback path must either prompt or error, never silently succeed.
- Phase semantics encoded in ad hoc strings. The JSON event schema must
  be defined before implementation.
- Confusing `--yes` with "skip the summary". `--yes` skips only the
  confirmation **prompt**; the summary is always printed for audit.

## Proposed work order

1. Define the JSON event schema + error envelope before coding the
   command flow. (~30 min)
2. Deprecate `--token` with a warning. (~15 min)
3. Build the `agentdetect` internal package (detector plugin interface +
   OpenClaw detector). (~1 hr)
4. Build the `setup` command:
   - Non-interactive path (the canonical one)
   - `--json` event emission against the defined schema
   - Interactive hidden token prompt via `golang.org/x/term` as a
     fallback
   - Confirmation summary + `--yes` / non-TTY auto-proceed policy
   - Idempotence + health check before success
   - (~3 hrs)
5. README rewrite: lead with the non-interactive bundled flow. Demote
   interactive to a development/one-off section. Demote `install` to "CI
   / power users". (~30 min)
6. Integration tests:
   - Non-interactive `setup` with env vars → asserts unit file + token
     file + `setup.completed` JSON envelope (~1 hr)
   - Idempotent rerun → asserts `action_taken: unchanged` (~30 min)
   - Token-rotation rerun → asserts `action_taken: updated` + service
     restart (~30 min)
7. `--json` event golden file tests. (~30 min)

Total estimate: ~half a day to a day of focused work.

## Decision

`setup` is **non-interactive-first** because the dominant caller is the
Loki installer. Interactive prompting remains supported only as fallback
input resolution for one-off operator use.
