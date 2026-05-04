# Privacy

## What we send at enroll time

`telemetron setup` may call `POST /v1/enroll` when no token source is already configured. The enroll request sends exactly:

- `schema`
- `install_id`
- `machine_id`
- `os`
- `arch`
- `source`
- `telemetron_version`
- `pack` (the configured mode, e.g. `openclaw`)
- `tier` (deployment tier, e.g. `internal` or `production`)

## What we send at flush time

Normal OTLP metric flushes send:

- the existing OTLP metric payload produced by `telemetron`
- `install_id` as an OTLP resource attribute
- `deployment_id` as an OTLP resource attribute (operator-configured identity of the deployment)
- `tier` as an OTLP resource attribute (operator-configured environment class: dev/staging/prod)
- `environment` as an OTLP resource attribute (operator-configured environment name)
- `pack_version` as an OTLP resource attribute (version of the pack being observed)

`deployment_id`, `tier`, `environment`, and `pack_version` come from the operator's config, not from the host. They never contain hostnames, usernames, or paths.

## What we never send

`telemetron` does not send:

- hostname
- username
- home-directory paths
- MAC addresses
- kernel version
- session content, prompts, responses, or tool payloads

## File permissions & rationale

`/etc/telemetron/token` is written `0400` because it is a bearer secret.

`/etc/telemetron/install-id` is written `0644` intentionally. The install id is an anonymous UUID, not a bearer credential. Keeping it world-readable lets support scripts, diagnostics, and operators inspect or report the installation identity without requiring `sudo`.

## Opt-out

Set `TELEMETRON_NO_AUTO_ENROLL=1` before running `telemetron setup` to skip anonymous enrollment entirely. In that mode, `setup` exits cleanly without starting the service unless some other token source is already present.
