# Contributing

Thanks for contributing to `telemetron`.

## Development setup

```bash
git clone https://github.com/inceptionstack/telemetron.git
cd telemetron
go mod download
make test
make build
```

Useful commands:

- `make test`
- `make lint`
- `go vet ./...`
- `go test -race ./...`
- `go build ./cmd/telemetron`

## Project conventions

- Keep changes small and reviewable.
- Add or update tests for behavioral changes.
- Update docs when flags, config, workflows, or operator behavior changes.
- Avoid hardcoded endpoints, account IDs, workspace IDs, usernames, or tokens.
- Prefer portable paths and `t.TempDir()` in tests.

## Commit style

Use conventional commits where practical, for example:

- `docs: rewrite README for OSS release`
- `refactor(openclaw): remove hardcoded session path`
- `chore(ci): add govulncheck job`

## Pull requests

Before opening a PR:

1. Run `go test ./...`.
2. Run `go build ./cmd/telemetron`.
3. Run `golangci-lint run`.
4. Update `CHANGELOG.md` when the change is user-visible.
5. Check docs links and examples.

PRs should include:

- A clear summary of the problem and fix.
- Test evidence.
- Any rollout or compatibility notes.

## Sign-off policy

Developer Certificate of Origin sign-off is recommended but not required. If you want to sign off commits, use `git commit -s`.

## Issues and labels

Look for `help wanted` issues if you want a scoped place to start. Bug reports and feature requests should use the GitHub issue templates.
