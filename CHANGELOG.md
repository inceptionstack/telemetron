# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-05-02

### Changed

- Refactored collector registration to self-registering specs with per-mode config decoding and a build-tagged demo collector.
- Split daemon management into a build-tagged `internal/service` layer, added Linux install ownership fixes, and improved status, version, and install CLI behavior.
- Hardened config, collector, OTLP sink, registry, and service test coverage while removing duplicated file-write and JSON helper logic.

## [0.1.0]

### Added

- Scaffolded the `clawtello` Go sidecar with `openclaw` collection mode.
- Added config loading, durable offsets, OTLP/HTTP protobuf export, systemd lifecycle, and status reporting.
- Added unit tests, Makefile targets, and GitHub Actions CI for test, lint, and module verification.
