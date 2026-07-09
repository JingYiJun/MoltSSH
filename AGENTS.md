# Project

MoltSSH is a Go CLI for an OpenSSH `ProxyCommand` transport with migratable connections. The current MVP is TCP/WebSocket-first; QUIC is optional after the WebSocket protocol is proven.

Repository artifacts are English-first: code, comments, commit messages, branch names, PR titles, PR descriptions, issue text, documentation, logs, errors, and test names.

Never put real private keys, tokens, certificates, host credentials, or machine-specific config in code, logs, tests, examples, README, or commits.

## Engineering Rules

- Keep implementations minimal; do not introduce provider, plugin, registry, dependency injection, configuration frameworks, or other structural abstractions until a second concrete implementation justifies them.
- The CLI binary name is fixed as `moltssh`.
- Prefer the Go standard library; introduce `github.com/quic-go/quic-go` only for QUIC integration.
- Add the smallest meaningful test for any non-trivial behavior change.
- Prove WebSocket resume and path switching before adding raw TCP, QUIC, relay, or fallback layers.

## Architecture

- Keep `cmd/moltssh` as a thin entrypoint only.
- Keep command parsing and user-facing CLI behavior in `internal/cli`.
- Keep WebSocket transport, TCP bridging, probing, resume, and transport lifecycle logic in `internal/tunnel`.
- Keep OpenSSH `ProxyCommand` compatibility as the primary integration constraint.
- Keep protocol messages simple and explicit; fail fast on invalid input instead of guessing or downgrading behavior.
- Keep security decisions explicit, especially around TLS verification, certificates, authentication, and host identity.
- Keep network shutdown paths predictable: close streams and connections intentionally and propagate useful errors.

## Code Style

- Keep code idiomatic Go; run `gofmt` before testing or committing.
- Prefer small functions with direct control flow.
- Return errors with useful context; do not log and return the same error in library/internal code.
- Respect `context.Context` for network operations and long-running work.
- Add comments only for non-obvious protocol behavior, security tradeoffs, concurrency, lifecycle, or compatibility constraints.
- Use short package names such as `cli` and `tunnel`; avoid generic names such as `manager`, `processor`, `helper`, or `util` unless the concept is genuinely generic.
- Keep CLI command and flag names lowercase kebab-case.

## Testing

- Prefer unit tests for parsing, protocol framing, error behavior, and lifecycle helpers.
- Add integration tests only when they verify real behavior that unit tests cannot cover.
- Tests must be deterministic and should avoid relying on a local `sshd` unless explicitly marked or isolated.
- Run `go test ./...` before committing. If the default Go cache is not writable, use `GOCACHE=/tmp/moltssh-go-build go test ./...`.

## Workflow

- Use Conventional Commits: `<type>(<optional-scope>): <summary>`. Allowed types: `feat`, `fix`, `refactor`, `docs`, `test`, `chore`, `ci`.
- Use lowercase kebab-case branch names with intent prefixes such as `feature/`, `fix/`, `docs/`, `test/`, `refactor/`, `chore/`, or `ci/`.
- Commit one logical change at a time; keep branches and PRs focused on one deliverable.
- PR descriptions should include what changed, why it changed, and how it was tested. Call out protocol, security, compatibility, or operational risks explicitly.
