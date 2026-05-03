# telemetron v0.3.0 — Enrollment Architecture

Final design (plan v6, Codex-accepted). Commit: `d2c438c` on branch `v0.3.0-enrollment-plan`.

---

## 1. Enrollment flow (happy path + retry + takeover attempt)

```mermaid
%%{init: {'theme':'base','themeVariables':{'primaryColor':'#f5f5f5','primaryTextColor':'#000','primaryBorderColor':'#333','lineColor':'#333','secondaryColor':'#e8e8e8','tertiaryColor':'#fafafa','noteTextColor':'#000','noteBkgColor':'#fffde7','noteBorderColor':'#333','actorTextColor':'#000','actorBkg':'#e3f2fd','actorBorder':'#333','signalColor':'#000','signalTextColor':'#000','labelTextColor':'#000','loopTextColor':'#000'}}}%%
sequenceDiagram
    autonumber
    participant L as Lowkey installer<br/>(install.sh)
    participant T as Telemetron<br/>(standalone)
    participant AGW as API Gateway<br/>telemetry.loki.run
    participant LE as lambda-enroll
    participant DDB as DynamoDB<br/>telemetron-enrollments

    rect rgb(200, 235, 200)
    Note over L,DDB: Happy path — first enroll
    L->>L: generate install_id (UUIDv4)<br/>+ machine_id (sha256)
    L->>AGW: POST /v1/enroll<br/>{install_id, machine_id, os, arch, ...}
    AGW->>LE: invoke
    LE->>LE: mint token<br/>lpk_enroll_[0-9a-f]{64}
    LE->>DDB: PutItem<br/>ConditionExpression=<br/>attribute_not_exists(install_id)
    DDB-->>LE: ok (atomic write)
    LE-->>AGW: 200 {token, install_id}
    AGW-->>L: 200
    L->>L: write /etc/telemetron/token (0400)<br/>+ /etc/telemetron/install-id (0644)
    end

    rect rgb(240, 230, 180)
    Note over T,DDB: Retry after client timeout (idempotent)
    T->>AGW: POST /v1/enroll (same install_id + machine_id)
    AGW->>LE: invoke
    LE->>DDB: PutItem ConditionExpression
    DDB-->>LE: ConditionalCheckFailedException
    LE->>DDB: GetItem(PK=install_id)
    DDB-->>LE: existing row (token, machine_id)
    LE->>LE: machine_id matches ✓
    LE-->>AGW: 200 {token: row.token}
    AGW-->>T: 200 (same token as before)
    end

    rect rgb(245, 200, 200)
    Note over T,DDB: Takeover attempt — blocked
    T->>AGW: POST /v1/enroll<br/>(guessed install_id,<br/>attacker machine_id)
    AGW->>LE: invoke
    LE->>DDB: PutItem ConditionExpression
    DDB-->>LE: ConditionalCheckFailedException
    LE->>DDB: GetItem(PK=install_id)
    DDB-->>LE: existing row
    LE->>LE: machine_id MISMATCH ✗
    LE-->>AGW: 409 Conflict (no mutation)
    AGW-->>T: 409
    end
```

---

## 2. Metrics flush & authorization (steady state)

```mermaid
%%{init: {'theme':'base','themeVariables':{'primaryColor':'#f5f5f5','primaryTextColor':'#000','primaryBorderColor':'#333','lineColor':'#333','secondaryColor':'#e8e8e8','tertiaryColor':'#fafafa','noteTextColor':'#000','noteBkgColor':'#fffde7','noteBorderColor':'#333','actorTextColor':'#000','actorBkg':'#e3f2fd','actorBorder':'#333','signalColor':'#000','signalTextColor':'#000','labelTextColor':'#000','loopTextColor':'#000'}}}%%
sequenceDiagram
    autonumber
    participant TX as telemetron<br/>(OTLP exporter)
    participant AGW as API Gateway
    participant AUTH as lambda-authorizer
    participant DDB as DynamoDB<br/>(GSI: token-hash-index<br/>INCLUDE: install_id, revoked, machine_id)
    participant LI as lambda-ingest
    participant FH as Firehose<br/>→ S3 / Athena
    participant AMP as Amazon Managed<br/>Prometheus

    TX->>TX: add resource attr<br/>install_id=<from /etc/...>
    TX->>AGW: POST /v1/metrics<br/>Bearer lpk_enroll_xxx<br/>OTLP body
    AGW->>AUTH: authorize
    AUTH->>AUTH: detect lpk_enroll_* prefix
    AUTH->>DDB: Query GSI token-hash-index<br/>WHERE token_hash=sha256(bearer)
    Note right of DDB: GSI projection excludes `token`.<br/>Response has only install_id,<br/>revoked, machine_id.
    DDB-->>AUTH: {install_id, revoked, machine_id}
    AUTH->>AUTH: revoked == false? ✓
    AUTH-->>AGW: allow +<br/>enrolled_install_id<br/>(signed ctx)

    AGW->>LI: invoke + ctx.enrolled_install_id
    LI->>LI: 1. strip client install_id<br/>from OTLP resource attrs
    LI->>LI: 2. inject enrolled_install_id<br/>(authoritative)
    LI->>LI: 3. promote install_id<br/>to top-level column

    par Warehouse path
      LI->>FH: PutRecord<br/>{install_id, metrics, ...}
      FH->>FH: S3 → Athena
    and AMP mirror
      LI->>LI: attrs.pop("install_id", None)
      LI->>AMP: remote_write<br/>(no install_id label)
    end
```

---

## 3. DynamoDB key design & IAM boundaries

```mermaid
%%{init: {'theme':'base','themeVariables':{'primaryColor':'#f5f5f5','primaryTextColor':'#000','primaryBorderColor':'#333','lineColor':'#333','secondaryColor':'#e8e8e8','tertiaryColor':'#fafafa','noteTextColor':'#000','noteBkgColor':'#fffde7','noteBorderColor':'#333','actorTextColor':'#000','actorBkg':'#e3f2fd','actorBorder':'#333','signalColor':'#000','signalTextColor':'#000','labelTextColor':'#000','loopTextColor':'#000'}}}%%
graph TB
    classDef default fill:#fff,stroke:#333,color:#000,stroke-width:1px
    subgraph DDB["DynamoDB: telemetron-enrollments"]
        BASE["Base table<br/>────────────────<br/>PK: install_id<br/>────────────────<br/>token (plaintext, AES-256 at rest)<br/>token_hash<br/>machine_id<br/>os, arch, source<br/>revoked, revoked_at<br/>created_at"]
        GSI["GSI: token-hash-index<br/>────────────────<br/>PK: token_hash<br/>────────────────<br/>Projection: INCLUDE<br/>install_id<br/>revoked<br/>machine_id<br/><br/>❌ token NOT projected"]
        BASE -.indexes.-> GSI
    end

    subgraph IAM["IAM Access Boundaries"]
        ENROLL["lambda-enroll<br/>────────────<br/>GetItem, PutItem, UpdateItem<br/>on BASE table<br/>✅ can read token<br/>(needed for idempotent retry)"]
        AUTHZ["lambda-authorizer<br/>────────────<br/>Query on GSI only<br/>❌ no base-table access<br/>❌ cannot read token"]
        INGEST["lambda-ingest<br/>────────────<br/>no DDB access at all<br/>gets enrolled_install_id<br/>from authorizer context"]
        OPERATOR["Operator IAM role<br/>────────────<br/>Query GSI + UpdateItem BASE<br/>(for revocation runbook)<br/>❌ cannot read token"]
    end

    ENROLL -->|GetItem / PutItem / UpdateItem| BASE
    AUTHZ -->|Query| GSI
    OPERATOR -->|Query| GSI
    OPERATOR -->|UpdateItem SET revoked=true| BASE
    INGEST -.->|no direct access| DDB

    classDef danger fill:#fdd,stroke:#c33,color:#000
    classDef safe fill:#dfd,stroke:#3a3,color:#000
    classDef neutral fill:#ddf,stroke:#33c,color:#000
    class ENROLL danger
    class AUTHZ,INGEST,OPERATOR safe
```

---

## 4. Repository / component map

```mermaid
%%{init: {'theme':'base','themeVariables':{'primaryColor':'#f5f5f5','primaryTextColor':'#000','primaryBorderColor':'#333','lineColor':'#333','secondaryColor':'#e8e8e8','tertiaryColor':'#fafafa','noteTextColor':'#000','noteBkgColor':'#fffde7','noteBorderColor':'#333','actorTextColor':'#000','actorBkg':'#e3f2fd','actorBorder':'#333','signalColor':'#000','signalTextColor':'#000','labelTextColor':'#000','loopTextColor':'#000'}}}%%
graph LR
    classDef default fill:#fff,stroke:#333,color:#000,stroke-width:1px
    subgraph L["inceptionstack/lowkey"]
        INSTALLSH["install.sh<br/>(existing + 1 new enroll call)"]
    end

    subgraph T["inceptionstack/telemetron"]
        TSETUP["cmd/telemetron/setup.go<br/>(existing)"]
        TENROLL["internal/enroll/<br/>(new)"]
        TEXPORT["internal/otlp/exporter.go<br/>(+1 resource attr)"]
        TSHARED["internal/machineid/<br/>(shared algorithm spec +<br/>frozen test vectors)"]
    end

    subgraph D["inceptionstack/loki-dashboard/infra/loki-telemetry"]
        INSTALL_LAMBDA["lambda-install<br/>(existing, unchanged)"]
        INGEST_LAMBDA["lambda-ingest<br/>(existing + binding logic)"]
        ENROLL_LAMBDA["lambda-enroll<br/>(NEW)"]
        AUTHZ_LAMBDA["lambda-authorizer<br/>(+ lpk_enroll_* path)"]
    end

    subgraph AWS["AWS"]
        APIGW["API Gateway<br/>telemetry.loki.run"]
        DDB_T["DynamoDB<br/>telemetron-enrollments"]
        WAF["WAF rate limits"]
        FH["Firehose → S3"]
        ATHENA["Athena<br/>(dashboard joins)"]
    end

    INSTALLSH -->|existing /v1/install| APIGW
    INSTALLSH -->|NEW /v1/enroll| APIGW
    INSTALLSH -.writes.-> TSETUP

    TSETUP --> TENROLL
    TENROLL -->|/v1/enroll<br/>&#40;standalone&#41;| APIGW
    TEXPORT -->|/v1/metrics<br/>w/ install_id| APIGW
    TSHARED -.algorithm.-> TENROLL
    TSHARED -.same alg.-> INSTALLSH

    APIGW --> WAF
    APIGW --> AUTHZ_LAMBDA
    APIGW --> ENROLL_LAMBDA
    APIGW --> INGEST_LAMBDA
    APIGW --> INSTALL_LAMBDA

    ENROLL_LAMBDA --> DDB_T
    AUTHZ_LAMBDA --> DDB_T
    INGEST_LAMBDA --> FH
    FH --> ATHENA

    classDef new fill:#cfc,stroke:#3a3,stroke-width:2px,color:#000
    classDef modified fill:#ffc,stroke:#aa3,stroke-width:2px,color:#000
    classDef existing fill:#eee,stroke:#888,color:#000
    class ENROLL_LAMBDA,TENROLL,TSHARED,DDB_T new
    class INSTALLSH,TEXPORT,AUTHZ_LAMBDA,INGEST_LAMBDA,TSETUP modified
    class INSTALL_LAMBDA,APIGW,WAF,FH,ATHENA existing
```

Legend:
- 🟢 green = new in v0.3.0
- 🟡 yellow = modified in v0.3.0
- ⚪ grey = existing, unchanged

---

## 5. Data correlation (dashboard)

```mermaid
%%{init: {'theme':'base','themeVariables':{'primaryColor':'#f5f5f5','primaryTextColor':'#000','primaryBorderColor':'#333','lineColor':'#333','secondaryColor':'#e8e8e8','tertiaryColor':'#fafafa','noteTextColor':'#000','noteBkgColor':'#fffde7','noteBorderColor':'#333','actorTextColor':'#000','actorBkg':'#e3f2fd','actorBorder':'#333','signalColor':'#000','signalTextColor':'#000','labelTextColor':'#000','loopTextColor':'#000'}}}%%
graph LR
    classDef default fill:#fff,stroke:#333,color:#000,stroke-width:1px
    subgraph Sources["Telemetry sources"]
        LOWKEY["lowkey install.sh<br/>beacon"]
        TX["telemetron<br/>(continuous flushes)"]
    end

    subgraph Tables["Data lake tables"]
        INSTALL["lowkey_install_v1<br/>install_id · pack · profile ·<br/>outcome · machine_id · ..."]
        METRICS["telemetron_metrics<br/>install_id (top-level col) ·<br/>metric_name · value · ingest_time"]
    end

    subgraph Join["Dashboard join"]
        Q["SELECT i.pack, t.metric_name, t.value<br/>FROM lowkey_install_v1 i<br/>JOIN telemetron_metrics t<br/>  ON i.install_id = t.install_id<br/>WHERE i.outcome='completed'"]
    end

    LOWKEY -->|POST /v1/install| INSTALL
    TX -->|POST /v1/metrics<br/>&#40;install_id rebound server-side&#41;| METRICS
    INSTALL --> Q
    METRICS --> Q

    classDef key fill:#fec,stroke:#c80,stroke-width:2px,color:#000
    class Q key
```

---

## Full v0.3.0 review history

| Version | Blocker Codex caught | Fix |
|---|---|---|
| v1 | Rebuilt infra that already exists in lowkey | Reuse telemetry.loki.run platform |
| v2 | Overwrite on machine_id mismatch = takeover primitive | 409 Conflict, no mutation |
| v3 | Query+PutItem not atomic under concurrency | `ConditionExpression=attribute_not_exists(install_id)` |
| v4 | Retry unimplementable (can't reverse a hash) | Store plaintext `token` in base row |
| v5 | GSI `projection:ALL` leaked token to authorizer | `projection: INCLUDE` (token excluded) + IAM boundary table |
| **v6** | **No findings** | **ACCEPT** |

Scope: 11 tasks, ~17 hours, ~2 days.
