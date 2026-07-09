# MoltSSH

MoltSSH is an experimental OpenSSH `ProxyCommand` transport that keeps an SSH
byte stream alive while the active network path moves between direct access and
TCP/HTTP relay paths.

The current MVP is TCP/WebSocket-first. Existing relay tools provide
reachability; MoltSSH provides session resume, path probing, and path switching.
UDP/QUIC is optional future work after the WebSocket protocol is proven.

See [docs/mvp.md](docs/mvp.md) for the active implementation plan and
[ADR 0001](docs/adr/0001-tcp-ws-primary-quic-auxiliary.md) for the transport
decision.

## Status

The WebSocket MVP is implemented:

- TOML-only `proxy`, `server`, and `probe` commands.
- WebSocket subprotocol `moltssh.v1` with framed `hello`, `accept`, `data`,
  `ack`, `fin`, `ping`, `pong`, `close`, and `error` messages.
- Server-side TCP bridging that keeps the target connection open across client
  reconnects until `resume.timeout`.
- In-memory replay buffers, epochs, offset checks, and probe-driven path
  selection/switching.

## Goals

- Work as an OpenSSH `ProxyCommand`.
- Carry the raw SSH stdin/stdout byte stream over WebSocket paths.
- Keep the server side connected to a local `sshd`, usually `127.0.0.1:22`.
- Preserve established SSH sessions across supported client reconnects and path
  switches.

## Non-goals

- It is not a VPN.
- It is not a general bastion host.
- It does not build a new relay system in the MVP.
- It does not survive client or server process restarts.

## CLI

Runtime choices belong in TOML config, not flags:

```bash
moltssh proxy  --config ~/.config/moltssh/lab.toml
moltssh server --config /etc/moltssh/lab.toml
moltssh probe  --config ~/.config/moltssh/lab.toml
```

OpenSSH example:

```sshconfig
Host lab-box
  HostName ignored
  User jingyijun
  ProxyCommand moltssh proxy --config ~/.config/moltssh/lab.toml
```

## Development

Requirements:

- Go 1.26.5+

Run checks:

```bash
go test ./...
go test -race ./...
scripts/docker-ssh-smoke.sh
```

If your environment cannot write to the default Go build cache, use a writable
cache path:

```bash
GOCACHE=/tmp/moltssh-go-build go test ./...
```

## Project layout

```text
cmd/moltssh/        CLI entrypoint
internal/cli/       command parsing
internal/tunnel/    WebSocket transport, TCP bridging, probing, and resume
```

## License

MIT License. See [LICENSE](LICENSE).
