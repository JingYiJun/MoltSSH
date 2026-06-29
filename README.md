# MoltSSH

MoltSSH is an experimental OpenSSH `ProxyCommand` transport that aims to keep an SSH byte stream alive while the underlying network path moves between LAN direct access and a public relay.

The first implementation target is Go + QUIC, using `quic-go` for connection IDs, ordered reliable streams, TLS 1.3, congestion control, retransmission, NAT rebinding, and path migration. A TCP/WebSocket resume layer is intentionally deferred until UDP/QUIC is proven insufficient.

## Status

MoltSSH is a fresh project skeleton. The CLI shape is in place; the QUIC transport is not implemented yet.

## Goals

- Work as an OpenSSH `ProxyCommand`.
- Carry the raw SSH stdin/stdout byte stream over one QUIC bidirectional stream.
- Prefer LAN UDP paths and fall back to public UDP relay paths.
- Preserve established SSH sessions across supported QUIC path migration.
- Keep the server side connected to a local `sshd`, usually `127.0.0.1:22`.

## Non-goals

- It is not a VPN.
- It is not a general bastion host.
- It does not reimplement TCP in the MVP.
- It does not promise survival across client/server process restarts.

## CLI

```bash
moltssh proxy  --config moltssh.yaml
moltssh server --listen :4433 --connect 127.0.0.1:22
moltssh probe  --config moltssh.yaml
```

OpenSSH example:

```sshconfig
Host lab-box
  HostName ignored
  User jingyijun
  ProxyCommand /usr/local/bin/moltssh proxy --config ~/.ssh/moltssh.yaml
```

Current skeleton commands print parsed options only:

```bash
go run ./cmd/moltssh proxy --config test.yaml
go run ./cmd/moltssh server --listen :4433 --connect 127.0.0.1:22
go run ./cmd/moltssh probe --config test.yaml
```

## Development

Requirements:

- Go 1.22+

Run checks:

```bash
go test ./...
```

If your environment cannot write to the default Go build cache, use a writable cache path:

```bash
GOCACHE=/tmp/moltssh-go-build go test ./...
```

## Project layout

```text
cmd/moltssh/        CLI entrypoint
internal/cli/       command parsing skeleton
```

## License

MIT License. See [LICENSE](LICENSE).
