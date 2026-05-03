# telemetron auto-enrollment plan (v0.3.0)

Status: **proposal — pending Codex review**
Author: Loki@FastStart
Date: 2026-05-03
Context: Follow-up to v0.2.0 E2E. Roy's goal: "users get automatic bearer tokens for telemetry when they start an install of Loki, without authentication — Loki is open source."

## Problem

Today, telemetron requires operators to pre-stage a bearer token via one of:

- `--token-file <path>`
- `TELEMETRON_TOKEN_FILE` env
- `TELEMETRON_TOKEN_SECRET=<aws-sm-arn>` (fetched by `install.sh`)

None of these work for an open-source Loki user on a fresh laptop or VPS who just ran `curl .../install.sh | sh`. They have no FastStart account, no AWS role, no pre-provisioned secret — yet we still want their telemetry to flow so we can improve the product.

## Constraint space

- **No user authentication** — Loki is OSS, anyone can install without registering
- **No hardcoded shared secret in the installer** — it would be in every download, extractable in seconds, useless as auth
- **Telemetry must still be trustworthy** — spammers must not be able to flood the ingester with garbage metrics
- **Zero friction** — user runs `loki install` or `curl | sh`, telemetry starts working, no prompts
- **Privacy-respecting** — no PII leaked in resource attributes
- **Revocable** — an abuse install must be killable without affecting anyone else
- **Rotatable** — tokens shouldn't live forever
- **Backward-compatible** — existing `TELEMETRON_TOKEN_SECRET` path (enterprise/Loki@FastStart) keeps working

## Chosen approach: Anonymous per-install enrollment (Approach #1)

Every telemetron install generates a **local install-ID** (UUIDv7) on first run, calls a public `POST /v1/enroll` endpoint, and receives a narrow-scope bearer token tied to that install-ID. Server-side rate limits + quotas prevent abuse; individual install-IDs can be revoked.

### Why not the other approaches

- **Device-code OAuth (GitHub/Stripe-style):** Requires a human identity. Loki has no account model.
- **Loki-backend mints tokens:** Couples telemetron to Loki. Explicitly ruled out by `docs/telemetron-setup-plan.md` ("no Loki branding in telemetron").
- **AWS IAM-based claim:** Only works on AWS. Most OSS users aren't on AWS.
- **Proof-of-possession with per-install keypair:** The right long-term answer but requires changing the ingester from bearer auth to signature auth. Too big a change for v0.3.0; revisit in v0.4+.
- **No auth, pure rate-limiting:** Ingester data becomes untrusted. Not viable.
- **Invite/claim codes:** Reintroduces a manual step. Doesn't solve the problem.

## Design

### Client-side flow (telemetron)

On first `telemetron setup` (no existing token, no `TELEMETRON_TOKEN_SECRET`, no `--token-file`):

1. Generate `install_id` (UUIDv7) → write to `/etc/telemetron/install-id` mode 0644 (non-secret, used in log correlation)
2. Collect enrollment metadata (non-PII):
   - `os`, `arch`
   - `telemetron_version`
   - `detected_agent` (openclaw/claude-code/codex/none) — same detection `agentdetect` already does
   - `agent_version` if detected
   - SHA256(hostname + install_id + hardcoded salt) as a stable-but-opaque host fingerprint
3. `POST /v1/enroll` with the above + `install_id`
4. Receive `{ token, expires_at, quota }` → write token to `/etc/telemetron/token` mode 0400
5. Continue setup as today (write config, start service, probe health)

Background renewal:
- On every flush, if token age > 60d, call `/v1/renew` with current bearer → writes new token atomically
- If `/v1/renew` returns 401/403: re-enroll from scratch (with the same install-ID so history is preserved)

New subcommands:
- `telemetron enroll` — explicit enrollment (useful for testing and for the `install.sh` auto-step)
- `telemetron renew` — force renewal
- `telemetron whoami` — prints install-ID, token age, quota, endpoint

### Server-side flow

**New public route:** `POST /v1/enroll` on the existing API Gateway

- **No auth** — intentionally public
- **Rate limit:** 100 enrollments / hour per source IP (WAF rate-based rule)
- **Global rate limit:** 5000 enrollments / hour (CloudWatch alarm + auto-throttle at 80%)
- **Request body (JSON):**
  ```json
  {
    "install_id": "01J9V7...",
    "os": "linux",
    "arch": "arm64",
    "telemetron_version": "0.3.0",
    "detected_agent": "openclaw",
    "agent_version": "2026.5.2",
    "host_fingerprint": "sha256:abc..."
  }
  ```
- **Validation:** schema check + install_id not already revoked
- **Action:** mint bearer token (opaque random 32 bytes, base58-encoded, prefix `lpk_enroll_`)
- **DDB write:**
  ```
  PK = install_id
  token_hash = sha256(token)
  created_at, last_seen_at
  quota_override = null  (defaults apply)
  revoked = false
  metadata = {os, arch, agent, ...}
  ```
- **Response:**
  ```json
  {
    "token": "lpk_enroll_...",
    "expires_at": "2026-08-03T00:00:00Z",
    "quota": { "flushes_per_min": 60, "batch_metrics_max": 500 }
  }
  ```

**New authenticated route:** `POST /v1/renew`

- Auth: current bearer in `Authorization: Bearer ...`
- Action: verify token hash matches an active row, mint new token, invalidate old (keep overlap of 1h so rotating clients don't miss flushes)
- Response: same shape as enroll

**Existing route:** `POST /v1/metrics`

- Authorizer updated to accept both:
  - `lpk_live_*` (existing, long-lived, Loki@FastStart provisioned via Secrets Manager)
  - `lpk_enroll_*` (new, install-scoped, from enrollment flow)
- Per-install quota enforcement: check `install_id` claim against DDB `quota_override` or defaults
- Rate-limit per install_id: 60 flushes / min (heartbeat is 4/min, real flushes a handful more — 60 is ~10x headroom)

**New admin tool:** `telemetron-admin`

- `telemetron-admin revoke --install-id <uuid> [--reason "..."]` — sets `revoked=true`
- `telemetron-admin list-recent-enrollments --hours 1` — for spike investigation
- `telemetron-admin quota --install-id <uuid> --flushes-per-min 1000` — per-install override

### `install.sh` integration

Current logic (post-v0.2.0):
```
if TELEMETRON_TOKEN_SECRET set → fetch from SM → write token
elif TELEMETRON_TOKEN_FILE set → copy to /etc/telemetron/token
elif /etc/telemetron/token already exists → skip
else → refuse to auto-setup (user must supply token)
```

New logic:
```
if TELEMETRON_TOKEN_SECRET set → fetch from SM → write token (enterprise path)
elif TELEMETRON_TOKEN_FILE set → copy (manual override)
elif /etc/telemetron/token already exists → skip (idempotent reinstall)
elif TELEMETRON_NO_AUTO_ENROLL=1 → skip, warn user
else → auto-enroll via telemetron enroll
```

The auto-enroll step is gated behind a one-line notice in `install.sh` output:
```
[telemetron-install] no token supplied; auto-enrolling this install for telemetry
[telemetron-install]   install-id: 01J9V7...
[telemetron-install]   endpoint:   https://telemetry.faststart.internal/v1/enroll
[telemetron-install]   opt out:    re-run with TELEMETRON_NO_AUTO_ENROLL=1
```

### Privacy

**Never sent:**
- Hostname (only salted hash)
- MAC address
- Username
- Home directory paths
- Session content (unchanged — telemetron has never sent this)

**Sent but coarse:**
- OS / arch (os=linux, arch=arm64 only — not kernel version)
- Agent name + version
- Telemetron version
- Source IP (by API Gateway) — retained at server for 30d for rate-limit tuning, then dropped

**Opt-out:**
- `TELEMETRON_NO_AUTO_ENROLL=1` → no enrollment, no metrics (telemetron exits cleanly from setup)
- Retrospective opt-out: `telemetron-admin revoke` can be called by the install owner (they have the install-ID)

**Documentation:** a `docs/privacy.md` file lands in the same PR explicitly listing every attribute sent, retention period, and opt-out.

### Abuse handling

1. **Per-IP rate limit** at WAF layer — 100 enroll/hour
2. **Per-install rate limit** at authorizer — 60 flushes/min
3. **Global enrollment rate alarm** — CloudWatch metric + SNS alert if > 5000/hour
4. **Anomaly scoring Lambda** (nightly cron):
   - Flag install-IDs with suspicious patterns (hostname fingerprint collisions, impossibly-fast enrollments, identical OS+arch+agent from 1000 different IPs in 10min)
   - Auto-revoke high-confidence abusers; queue low-confidence for human review
5. **Kill switch** — `TELEMETRON_ENROLL_DISABLED=1` env on the enroll Lambda → returns 503 to all enroll requests, existing tokens keep working

### Backward compatibility

- Existing `lpk_live_*` tokens (Loki@FastStart via Secrets Manager) keep working unchanged
- Existing `TELEMETRON_TOKEN_SECRET` env path keeps working — takes precedence over auto-enroll
- Existing config files unchanged
- No schema changes to `/v1/metrics` payload

### Self-hosted / air-gapped users

Override endpoints via:
- `TELEMETRON_ENROLL_ENDPOINT` → defaults to FastStart's public enroll URL
- `TELEMETRON_METRICS_ENDPOINT` → existing
- `TELEMETRON_NO_AUTO_ENROLL=1` + `TELEMETRON_TOKEN_FILE=<local>` → fully offline telemetron with a user-managed token

## Implementation plan

| # | Task | Area | Effort |
|---|------|------|--------|
| 1 | DDB schema + table provisioning (CDK/CFN) | Infra | S (2h) |
| 2 | `POST /v1/enroll` Lambda + route + WAF rules | Backend | M (4h) |
| 3 | `POST /v1/renew` Lambda + route | Backend | S (2h) |
| 4 | Authorizer: accept `lpk_enroll_*`, per-install quota | Backend | M (3h) |
| 5 | `telemetron enroll` subcommand + install-ID persistence | Client | M (3h) |
| 6 | `telemetron renew` + background renewal loop | Client | S (2h) |
| 7 | `telemetron whoami` subcommand | Client | S (1h) |
| 8 | `install.sh` auto-enroll integration | Client | S (1h) |
| 9 | `telemetron-admin` revoke + list + quota | Backend/ops | M (3h) |
| 10 | Anomaly scoring cron Lambda | Backend | M (4h) |
| 11 | `docs/privacy.md` + CHANGELOG + README opt-out docs | Docs | S (2h) |
| 12 | E2E test: fresh install → auto-enroll → flush → revoke → flush fails | Test | S (2h) |

**Total:** ~29 hours = ~4 days of focused work.

## Test plan

- Unit: enroll-lambda schema validation, token minting, DDB round-trip
- Unit: `telemetron enroll` idempotency (re-running keeps the same install-ID)
- Unit: renewal logic (fresh token < 60d → skip; > 60d → renew; 401 → re-enroll)
- Integration: full flow on `oc-telemetron-e2e-test` stack — fresh box, `curl | sh`, verify `ingest_ok` with `lpk_enroll_*` prefix
- Load test: 1000 concurrent enrollments, verify WAF rate-limit kicks in, no partial DDB writes
- Abuse test: same IP spamming enroll → blocked at 101st attempt within an hour
- Revoke test: `telemetron-admin revoke` → next flush gets 403, `install.sh` re-enroll succeeds

## Open questions for Codex

1. **UUIDv7 vs ULID vs UUIDv4** for install-ID — UUIDv7 gives natural time-sorting; does that leak enrollment time in a way we'd regret later?
2. **Token prefix scheme** — `lpk_enroll_*` distinguishes from `lpk_live_*` but does it leak information about the install's provenance that we shouldn't leak?
3. **Quota defaults** — is 60 flushes/min the right number? Heartbeat is 4/min; most real workloads I can imagine don't exceed 10/min. 60 is ~15x headroom.
4. **Renewal window** — is 60d the right threshold? Too short = flaky laptop installs get orphaned; too long = revocation takes too long to propagate.
5. **Anomaly scoring false-positive rate** — how do we avoid auto-revoking legit CI systems that enroll from many different IPs (GitHub Actions runners, GitLab CI, etc.)?
6. **GDPR / data deletion** — if a user wants their telemetry deleted, what's the mechanism? Install-ID gives us a natural purge key.
7. **install.sh auto-enroll default** — should it be opt-in (`TELEMETRON_AUTO_ENROLL=1`) or opt-out (`TELEMETRON_NO_AUTO_ENROLL=1`)? My draft says opt-out. Is that the right call for an OSS project?
8. **Scope creep check** — should `telemetron-admin` ship in v0.3.0 or can it be v0.3.1? It's operational, not user-facing.

## Deferred to v0.4+

- Signature-based auth (per-install keypair, no shared bearer)
- Self-serve quota upgrades via a web dashboard
- Multi-tenant billing (per-org quotas for Loki@FastStart customers)
- Federated enroll endpoints (for corporate proxies / air-gap relays)

## Rollout

1. Codex review of this plan → revise
2. Implement tasks 1-12 on a feature branch
3. Deploy to a staging API Gateway + DDB first; run E2E against staging
4. Load test + abuse test
5. Cut v0.3.0-rc1, get it on a volunteer's laptop for 1 week
6. Promote to v0.3.0 stable
7. Update `openclaw-telemetron-e2e` skill to verify auto-enroll path
