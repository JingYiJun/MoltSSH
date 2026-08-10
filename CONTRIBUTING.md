# Contributing to MoltSSH

Thank you for helping improve MoltSSH. Keep contributions focused, testable,
and explicit about protocol, security, compatibility, and operational risks.

## Before You Start

- Search existing issues before filing a new report.
- Use the bug or feature issue template and include a reproducible network
  topology when behavior depends on relays, proxies, or TLS termination.
- Do not open public issues for vulnerabilities. Follow [SECURITY.md](SECURITY.md).
- Repository artifacts are English-first, including code, comments, commits,
  branches, pull requests, issues, documentation, logs, errors, and test names.

## Development Setup

Requirements:

- Go 1.26.5 or newer
- OpenSSH client
- Docker for the end-to-end SSH failover smoke test

Run the local quality gates:

```bash
gofmt -w ./cmd ./internal
scripts/check-go-pure-loc.sh
go vet ./...
go test ./...
go test -race -shuffle=on -count=1 ./...
scripts/docker-ssh-smoke.sh
```

Never weaken or remove a failing test to make a change pass.

## Branches and Commits

Use lowercase kebab-case branches with an intent prefix, for example:

```text
feature/ws-probe-output
fix/resume-offset-validation
docs/nginx-deployment
```

Use Conventional Commits with one logical change per commit:

```text
feat: improve probe output
fix: reject stale resume epochs
docs: add nginx deployment guide
```

Allowed types are `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, and `ci`.

## Pull Requests

Every pull request should include:

- What changed and why.
- The user-visible or protocol behavior affected.
- Exact commands used to test the change.
- Protocol, security, compatibility, and operational risks.
- Documentation updates for user-facing changes.

Keep `cmd/moltssh` as a thin entrypoint, CLI behavior in `internal/cli`, and
transport/session behavior in `internal/tunnel`. Prefer the Go standard library
and add the smallest meaningful test for non-trivial behavior.

By participating, you agree to follow [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).
