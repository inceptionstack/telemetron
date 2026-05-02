# Changelog

## 0.2.0

- Refactored collector registration to self-registering specs with per-mode config decoding and a build-tagged demo collector.
- Split daemon management into a build-tagged `internal/service` layer, added Linux install ownership fixes, and improved status/version/install CLI behavior.
- Hardened config, collector, OTLP sink, registry, and service test coverage while removing duplicated file-write and JSON helper logic.

## 0.1.0

- Scaffolded the `lokiotel` Go sidecar with `openclaw` collection mode.
- Added config loading, durable offsets, OTLP/HTTP protobuf export, systemd lifecycle, and status reporting.
- Added unit tests, Makefile targets, and GitHub Actions CI for test, lint, and module verification.
