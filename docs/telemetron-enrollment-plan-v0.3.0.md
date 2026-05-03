# telemetron auto-enrollment plan (v0.3.0) — REVISED

Status: **proposal v2 — pending Codex review**
Author: Loki@FastStart
Date: 2026-05-03
Supersedes: v1 of this doc (was overengineered; Codex + Roy critique led to this rewrite)

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
| 5 | No token renewal / revocation / admin tool in v0.3.0 | Happy path only. Rotation & revocation = v0.3.1. |
| 6 | No anomaly-scoring Lambda in v0.3.0 | Existing WAF + per-install rate limit is enough for launch. |
| 7 | Token prefix `lpk_enroll_*` (distinct from `lpk_live_*`) | Easy to differentiate in dashboards and authorizer. |
| 8 | When lowkey is installed, lowkey passes *its* existing `install_id` to `/v1/enroll`. Telemetron standalone generates its own UUIDv4. | No duplication. One identity per install regardless of origin. |

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
  1. Generate 32-byte random token → base58 → prefix `lpk_enroll_`
  2. DDB `PutItem` in new table `telemetron-enrollments`:
     ```
     PK = install_id
     token_hash = sha256(token)   # for authorizer lookups
     machine_id, os, arch, source, created_at
     ```
  3. Return `{"token": "lpk_enroll_...", "install_id": "<echoed>"}`
- **Rate limit:** reuse existing WAF rule pattern from `/v1/install` (per-IP 100/hour; global 5k/hour alarm)
- **Idempotency:** if the same `install_id` enrolls twice with a matching `machine_id`, return the existing token (no churn). Different `machine_id` → treat as a new install, mint a new token, overwrite.

### Existing: `POST /v1/metrics` (telemetron's endpoint)

**One change:** authorizer accepts both `lpk_live_*` (existing) and `lpk_enroll_*` (new). For `lpk_enroll_*`, authorizer does a DDB GetItem on `token_hash` to validate the token is an active row. That's the only added logic; no payload rewriting, no enrichment.

**No AMP propagation of install_id for v0.3.0:** the ingest Lambda forwards resource attrs to the warehouse (S3/Athena) but strips `install_id` from any AMP mirror. Codex's #6 privacy concern addressed with one `attrs.pop("install_id", None)` before the AMP PutRecords.

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
**Sent at flush time:** existing OTLP metrics + install_id as a resource attribute.
**Never sent:** hostname, username, home paths, MAC, kernel version, session content.

**Opt-out:** `TELEMETRON_NO_AUTO_ENROLL=1` → no enroll call, no metrics (setup exits cleanly, telemetron service never starts).

A `docs/privacy.md` ships in the same PR listing every attribute sent, retention, and opt-out.

## Backward compatibility

- Existing `lpk_live_*` tokens (Loki@FastStart via Secrets Manager) unchanged.
- `TELEMETRON_TOKEN_SECRET`, `TELEMETRON_TOKEN_FILE`, `TELEMETRON_TOKEN` — all unchanged and take precedence over enrollment.
- Existing `/v1/install` and `/v1/ingest` routes unchanged.
- OTLP wire format: adds one optional resource attribute; servers older than v0.3.0 simply ignore unknown attrs.

## Implementation plan

| # | Task | Area | Effort |
|---|------|------|--------|
| 1 | DDB table `telemetron-enrollments` (CDK) | Infra | XS (30m) |
| 2 | `lambda-enroll/handler.py` + route wiring + WAF reuse | Backend | S (2h) |
| 3 | Authorizer: accept `lpk_enroll_*` via DDB GetItem | Backend | S (1.5h) |
| 4 | Ingest: promote `install_id` to top-level col; strip from AMP mirror | Backend | S (1h) |
| 5 | Telemetron: enroll client (Go, in `internal/enroll/`) + setup integration | Client | M (3h) |
| 6 | Telemetron: exporter adds `install_id` resource attr | Client | XS (30m) |
| 7 | Lowkey `install.sh`: call `/v1/enroll` after `/v1/install`, write `/etc/telemetron/{token,install-id}` | Client | S (1h) |
| 8 | `docs/privacy.md`, CHANGELOG, README opt-out | Docs | S (1h) |
| 9 | E2E: fresh box → `curl lowkey install.sh \| sh` → verify telemetron flushes with install_id → Athena join returns a row | Test | S (2h) |

**Total: ~12.5 hours ≈ 1.5 days.** (Down from v1's ~29h estimate.)

## Deferred (v0.3.1+)

- Token renewal / rotation (60d threshold, `/v1/renew`)
- Revocation via `telemetron-admin` CLI
- Anomaly scoring Lambda
- Per-install quota overrides
- GDPR self-serve deletion endpoint
- UUIDv4 → UUIDv7 migration (time-sortable)
- Signature-based auth (v0.4+)

## Open questions for Codex

1. **Is the simplification aggressive enough?** The v1 plan had 12 tasks and ~29h. v2 has 9 tasks and ~12.5h. Am I over-cutting (e.g., no revocation at launch means a single abuse install can't be killed until we deploy a revoke tool)?
2. **Idempotency semantics of `/v1/enroll`:** same `install_id` + same `machine_id` → return existing token. Same `install_id` + different `machine_id` → mint new and overwrite. Is that the right policy, or should mismatched `machine_id` be a 409 Conflict?
3. **Client-side install_id injection:** I'm overriding your #2 recommendation in favor of Roy's #1 because install_id is anonymous. Agree that the AMP-strip in the ingest Lambda is sufficient mitigation for the "durable per-install label in metrics plane" concern?
4. **Token prefix `lpk_enroll_*` vs `lpk_live_*`:** you didn't flag this as a problem last round. Still fine?
5. **`machine_id` reuse across products:** lowkey already hashes it as `sha256(/etc/machine-id + ':' + hostname)`. Is there a risk in telemetron independently recomputing the same thing in the standalone path? (My read: no — the hash is the id; whoever computes it gets the same result.)
6. **Authorizer cold path:** every `/v1/metrics` for an `lpk_enroll_*` token now does a DDB GetItem. For `lpk_live_*` it still does the static pattern check. Is the bifurcation OK, or should both paths go through DDB for consistency?
7. **No renewal at launch:** tokens minted in v0.3.0 live forever until we ship renewal in v0.3.1. Is that an unacceptable debt, or fine for the first release?
8. **Anything material we're still missing?**

Please be direct. If the v2 plan is still wrong or still overbuilt, say so concretely.
