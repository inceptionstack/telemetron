# telemetron auto-enrollment plan (v0.3.0) — REVISED

Status: **proposal v3 — pending final Codex review**
Author: Loki@FastStart
Date: 2026-05-03
Supersedes: v2 (v2 architecture accepted; v3 fixes 4 Codex findings: idempotency, token↔install_id binding, DDB key design, token format contract)

## Problem

An OSS Loki user on a fresh box who runs `curl .../install.sh | sh` has no FastStart account, no AWS role, no pre-provisioned secret. Telemetron needs a bearer token anyway. Today it refuses to start in that case. Fix it.

## Key discovery (corrects v1 of this plan)

The `inceptionstack/lowkey` installer **already** generates the identity we need:

- `install_id` — UUIDv4, generated per install in [`install.sh:144`](https://github.com/inceptionstack/lowkey/blob/main/install.sh)
- `machine_id` — `sha256:` + sha256(machine-id + `:` + hostname), [`install.sh:171`](https://github.com/inceptionstack/lowkey/blob/main/install.sh)
- Both already posted to `https://telemetry.loki.run/v1/install` and `/v1/ingest`
- Validated by [`validate.py`](https://github.com/inceptionstack/loki-dashboard/blob/main/infra/loki-telemetry/lambda-ingest/_shared/validate.py) against strict schemas `lowkey.install.v1` / `lowkey.telemetry.v1`
- Consumed by `loki-dashboard` Athena queries

v1 of this plan proposed rebuilding all of this inside telemetron. It shouldn't.

## Chosen approach (revised)

**Reuse the existing `telemetry.loki.run` platform. Add one new enrollment route alongside the existing install/ingest routes. Client sends `install_id` as a resource attribute on every metric flush.**

### Architectural decisions

| # | Decision | Why |
|---|----------|-----|
| 1 | New route `POST /v1/enroll` on the same API Gateway as `/v1/install` and `/v1/ingest` | Reuse infra, schema, rate-limit, Firehose. Codex: "this is the correct option." |
| 2 | New Lambda (`lambda-enroll`) parallel to `lambda-install` / `lambda-ingest`, same repo (`loki-dashboard/infra/loki-telemetry`) | Keep handler semantics clean. Codex: "do not overload lambda-install." |
| 3 | Client sends `install_id` as OTel resource attribute on every flush | Roy: "It's anonymous. It's just a number. That gives us the correlation." Codex preferred server-side enrichment; we accept the simpler client-side approach because install_id carries no identity. |
| 4 | `install_id` primary join key; `machine_id` secondary | Codex: `machine_id` can drift when hostname changes; install_id is the stable identity. |
| 5 | **Operator-only revocation in v0.3.0** (DDB `revoked=true` flag, authorizer rejects). No user-facing CLI. Rotation = v0.3.1. | Codex: revocation must exist from day one; user-facing tool can wait. |
| 6 | No anomaly-scoring Lambda in v0.3.0 | Existing WAF + per-install rate limit is enough for launch. |
| 7 | Token prefix `lpk_enroll_*` (distinct from `lpk_live_*`) | Easy to differentiate in dashboards and authorizer. |
| 8 | When lowkey is installed, lowkey passes *its* existing `install_id` to `/v1/enroll`. Telemetron standalone generates its own UUIDv4. | No duplication. One identity per install regardless of origin. |
| 9 | **Token = `lpk_enroll_` + lowercase hex of 32 random bytes (64 hex chars)**. Matches existing `lpk_[a-z]+_[0-9a-f]+` scanner regex exactly. | Keeps git-secrets / CI patterns uniform with `lpk_live_*`. No base58 — Codex flagged the format drift. |
| 10 | **Ingest binds token → enrolled install_id server-side** — ingest Lambda looks up token's enrolled `install_id` and overwrites any client-supplied value before warehousing | Codex: client-supplied `install_id` in OTLP can poison joins without this bind. Non-negotiable. |
| 11 | **DDB table has primary key `token_hash` + GSI `install_id-index`** | Authorizer looks up by token_hash; enroll idempotency check looks up by install_id. Both paths are O(1). |

## Client flow

### Telemetron standalone (no lowkey)

On first `telemetron setup` with no token sources present:

1. Generate `install_id` (UUIDv4, `/proc/sys/kernel/random/uuid` or `crypto/rand`)
2. Write to `/etc/telemetron/install-id` (mode 0644, non-secret)
3. Compute `machine_id` using the **same algorithm as lowkey** (`sha256:` + sha256(`/etc/machine-id` or fallback + `:hostname`)) — so a standalone telemetron install can later be correlated by machine_id if the box also gets lowkey installed
4. `POST /v1/enroll` with `{install_id, machine_id, os, arch, telemetron_version}`
5. Receive `{token, install_id}` → write token to `/etc/telemetron/token` (mode 0400)
6. Continue normal setup

### Lowkey-bundled (most common case)

`install.sh` in the lowkey repo:

1. Generates `install_id` + `machine_id` as it does today (unchanged)
2. After the existing `/v1/install` beacon, makes a **second** call: `POST /v1/enroll` with the same `install_id` + `machine_id`
3. Writes the returned token to `/etc/telemetron/token` **AND** writes the install_id to `/etc/telemetron/install-id`
4. Calls `telemetron setup` which finds both files already staged, skips enrollment, starts the service

Telemetron's setup logic needs **one** change:

```
if /etc/telemetron/token exists → use it (today's behavior, unchanged)
elif TELEMETRON_TOKEN_SECRET / TELEMETRON_TOKEN_FILE / TELEMETRON_TOKEN → use it (today's behavior)
elif TELEMETRON_NO_AUTO_ENROLL=1 → skip, exit cleanly  (NEW)
else → call POST /v1/enroll, write /etc/telemetron/token + /etc/telemetron/install-id  (NEW)
```

### OTLP flush (new behavior)

Exporter reads `/etc/telemetron/install-id` once at startup and adds it as a resource attribute on every metric batch:

```
resource.attributes["install_id"] = <uuid>
```

That's the only wire change. The bearer still authenticates; install_id is a correlation key, not an auth claim.

## Server flow

### New: `POST /v1/enroll`

- **Lambda:** `lambda-enroll/handler.py` in `loki-dashboard/infra/loki-telemetry/`
- **Auth:** none (intentionally public)
- **Request:**
  ```json
  {
    "schema": "lowkey.enroll.v1",
    "install_id": "<uuidv4>",
    "machine_id": "sha256:<64hex>",
    "os": "linux",
    "arch": "arm64",
    "telemetron_version": "0.3.0",
    "source": "lowkey-installer" | "telemetron-standalone"
  }
  ```
- **Validation:** reuse `_shared/validate.py` regex primitives (`UUID_RE`, `MACHINE_ID_RE`, `ALLOWED_OS`, `ALLOWED_ARCH`)
- **Action:**
  1. **Idempotency check:** `Query GSI install_id-index WHERE install_id = :req`.
     - No row → proceed to mint (first enrollment)
     - Row exists with matching `machine_id` → return existing row's token (idempotent retry). Does **not** re-mint. Required so a client that times out after mint but before receiving the response can safely retry.
     - Row exists with different `machine_id` → **return `409 Conflict`, no mutation.** Client-supplied `install_id` must not be an account-takeover primitive. Codex-required.
  2. Mint token: `lpk_enroll_` + lowercase hex of 32 random bytes (`crypto/rand`). 64 hex chars total (11 prefix + 64 hex = 75 chars). Regex: `lpk_enroll_[0-9a-f]{64}`.
  3. DDB `PutItem` in new table `telemetron-enrollments`:
     ```
     PK        = token_hash   (sha256 of the token, lowercase hex, 64 chars)
     GSI1 PK   = install_id   (name: install_id-index, projection: ALL)
     attrs     = machine_id, os, arch, source, telemetron_version, created_at,
                 revoked (bool, default false), revoked_at (nullable)
     ```
  4. Return `{"token": "lpk_enroll_...", "install_id": "<echoed>"}`
- **Rate limit:** reuse existing WAF rule pattern from `/v1/install` (per-IP 100/hour; global 5k/hour alarm)
- **Retry safety:** because idempotency returns the existing token on exact match, a client that times out after server mint but before receiving the HTTP response can retry with the same `install_id` + `machine_id` and get the same token back. No orphaned rows, no duplicate tokens.

### Existing: `POST /v1/metrics` (telemetron's endpoint)

**Authorizer changes:**
- Accepts both `lpk_live_*` (existing, regex match, no DDB lookup)
- And `lpk_enroll_*` (new) — on match, `GetItem(PK=sha256(token))`. Accepts only if row exists **AND** `revoked == false`. 401 otherwise.
- On accept, the authorizer passes `enrolled_install_id` (from DDB row) to the ingest Lambda via a signed request context attribute.

**Ingest Lambda changes (Codex-required):**
1. Strip any client-supplied `install_id` from the incoming OTLP resource attrs.
2. Replace with the authoritative `enrolled_install_id` from the authorizer context. This is the server-side binding that prevents join poisoning.
3. Promote `install_id` from the resource-attrs map to an explicit top-level column on the Firehose record (for clean Athena joins).
4. Before any AMP mirror: `attrs.pop("install_id", None)` — no durable per-install label in Prometheus plane.

**Revocation path (operator-only, v0.3.0):**
- Ops runs: `aws dynamodb update-item --table telemetron-enrollments --key '{"token_hash":{"S":"..."}}' --update-expression 'SET revoked = :t, revoked_at = :n' --expression-attribute-values '{":t":{"BOOL":true},":n":{"S":"<iso8601>"}}'`
- Next flush from that install returns 401.
- No user-facing CLI in v0.3.0 (Codex explicitly said this is acceptable). CLI wrapper = v0.3.1.

### Dashboard

Athena JOIN:
```sql
SELECT i.install_id, i.pack, i.profile, t.metric_name, t.value, t.ingest_time_utc
FROM   lowkey_install_v1 i
JOIN   telemetron_metrics t ON i.install_id = t.install_id
WHERE  i.outcome = 'completed'
```

For this to work cleanly, ingest promotes `install_id` from the OTel resource-attrs map to an **explicit top-level column** in the Firehose record (Codex's #5 warning — don't rely on nested-attr extraction in Athena).

## Privacy

**Sent at enroll time:** install_id, machine_id (salted hash), os, arch, telemetron_version, source.
**Sent at flush time:** existing OTLP metrics + install_id as a resource attribute (which the server will overwrite with the authoritative bound value — client's copy is a join hint only, never trusted).
**Never sent:** hostname, username, home paths, MAC, kernel version, session content.

**Opt-out:** `TELEMETRON_NO_AUTO_ENROLL=1` → no enroll call, no metrics (setup exits cleanly, telemetron service never starts).

**File permissions:**
- `/etc/telemetron/token` → `0400` (bearer, secret)
- `/etc/telemetron/install-id` → `0644` (anonymous UUID, intentionally world-readable so non-root support scripts / `telemetron whoami` / troubleshooting ticket helpers can cite it without sudo). Documented in `docs/privacy.md`.

A `docs/privacy.md` ships in the same PR listing every attribute sent, retention, and opt-out.

## Backward compatibility

- Existing `lpk_live_*` tokens (Loki@FastStart via Secrets Manager) unchanged.
- `TELEMETRON_TOKEN_SECRET`, `TELEMETRON_TOKEN_FILE`, `TELEMETRON_TOKEN` — all unchanged and take precedence over enrollment.
- Existing `/v1/install` and `/v1/ingest` routes unchanged.
- OTLP wire format: adds one optional resource attribute; servers older than v0.3.0 simply ignore unknown attrs.

## Implementation plan

| # | Task | Area | Effort |
|---|------|------|--------|
| 1 | DDB table `telemetron-enrollments` + GSI `install_id-index` (CDK) | Infra | XS (45m) |
| 2 | `lambda-enroll/handler.py`: idempotent enroll, 409 on machine_id mismatch, WAF reuse | Backend | M (3h) |
| 3 | Authorizer: accept `lpk_enroll_*` via DDB GetItem by token_hash + check `revoked` flag | Backend | S (2h) |
| 4 | Ingest: strip client install_id, inject bound install_id from authorizer ctx, promote to top-level col, strip from AMP mirror | Backend | S (2h) |
| 5 | Shared spec + frozen test vectors for `machine_id` algorithm (so lowkey + telemetron cannot drift) | Backend/Client | XS (45m) |
| 6 | Telemetron: enroll client (Go, in `internal/enroll/`) + setup integration + retry-on-timeout idempotent semantics | Client | M (3h) |
| 7 | Telemetron: exporter adds `install_id` resource attr | Client | XS (30m) |
| 8 | Lowkey `install.sh`: call `/v1/enroll` after `/v1/install`, write `/etc/telemetron/{token,install-id}` | Client | S (1h) |
| 9 | Update `git-secrets` patterns: add `lpk_enroll_[0-9a-f]{64}` alongside `lpk_live_[0-9a-f]{32}` | Ops | XS (15m) |
| 10 | `docs/privacy.md`, CHANGELOG, README opt-out, operator revocation runbook | Docs | S (1.5h) |
| 11 | E2E: fresh box → `curl lowkey install.sh \| sh` → verify telemetron flushes with bound install_id → Athena join returns a row → operator revoke → next flush 401 | Test | S (2.5h) |

**Total: ~17 hours ≈ 2 days.** (Up slightly from v2's 12.5h because of binding + revocation + 409 work, but still well under v1's 29h.)

## Deferred (v0.3.1+)

- Token renewal / rotation (60d threshold, `/v1/renew`)
- Revocation via `telemetron-admin` CLI
- Anomaly scoring Lambda
- Per-install quota overrides
- GDPR self-serve deletion endpoint
- UUIDv4 → UUIDv7 migration (time-sortable)
- Signature-based auth (v0.4+)

## Changes since v2 (Codex-driven)

| Codex finding | Fix in v3 |
|---|---|
| Overwrite on mismatched `machine_id` is a takeover primitive | Now `409 Conflict`, no mutation |
| Client-supplied `install_id` poisons joins unless server rebinds | Ingest Lambda strips client value, injects authoritative bound value from authorizer context |
| Token format drifts from existing `lpk_[a-z]+_[0-9a-f]+` scanner regex | Tokens = `lpk_enroll_` + 64 hex chars (no base58). Scanner pattern added explicitly |
| DDB key/index design wrong — authorizer wants `token_hash` lookup but PK was `install_id` | PK = `token_hash`, GSI `install_id-index` for enroll idempotency |
| Revocation needs to exist from day one even without a user CLI | `revoked` boolean in DDB + authorizer check + operator runbook |
| Retry-after-mint semantics not spelled out | Idempotent re-enroll returns same token on exact match |
| `machine_id` algorithm drift risk between lowkey and telemetron | Shared spec + frozen test vectors (Task #5) |
| `/etc/telemetron/install-id` permissions unclear | `0644` with rationale in privacy.md |

## Open questions for Codex (final round)

1. Does v3 correctly close all four must-fix findings from your v2 review (idempotency, token-install binding, DDB key design, token format contract)?
2. Is the `revoked` flag + operator runbook sufficient as a day-one revocation mechanism, or do you want a dedicated admin Lambda behind IAM auth in v0.3.0?
3. Token format `lpk_enroll_[0-9a-f]{64}` (32 random bytes hex-encoded) — strong enough for a long-lived token, or do you want more entropy?
4. Anything still missing or still wrong?

If v3 is acceptable, say so explicitly and I'll hand it to Codex for implementation on branch `v0.3.0-enrollment-plan`.
