# Security Policy

## Supported Versions

Security fixes are provided for the latest tagged release. Older `0.x`
releases may be asked to upgrade before a report is investigated.

## Reporting a Vulnerability

Do not file a public issue for a suspected vulnerability.

Use GitHub private vulnerability reporting when it is available for this
repository. Otherwise, contact the repository maintainer through a private
contact method on the maintainer's GitHub profile. Include:

- The affected MoltSSH version or commit.
- A minimal reproduction with secrets and private infrastructure removed.
- The expected security boundary and observed impact.
- Whether the issue is already public or under active exploitation.

The maintainer will acknowledge a complete report as availability permits,
coordinate a fix and disclosure timeline, and credit reporters who request it.

## Deployment Boundary

MoltSSH is a resumable byte-stream transport for OpenSSH. It is not an SSH
server, VPN, identity provider, or public relay.

- The MoltSSH WebSocket server has no application-layer authentication.
- The server does not terminate TLS. Use an authenticated reverse proxy,
  private access layer, or other protected tunnel for public relay paths.
- Prefer a loopback `server.listen` address. The CLI warns when the listener is
  not loopback.
- `ws://` is only appropriate on trusted localhost, LAN, or already-protected
  paths. Use `wss://` across untrusted networks.
- A MoltSSH server restart terminates in-memory sessions and the stable TCP
  connections they own.
- Keep private keys, credentials, certificates, tokens, endpoints containing
  secrets, and real host details out of issues and logs.

See the README troubleshooting section and [docs/mvp.md](docs/mvp.md) for the
full MVP security model and protocol limits.
