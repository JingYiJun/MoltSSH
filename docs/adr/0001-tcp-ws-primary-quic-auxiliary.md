# ADR 0001: Use WebSocket Resume as the MVP Transport

## Status

Accepted

## Context

MoltSSH originally targeted QUIC path migration between LAN and relay paths.
That is still useful when UDP is available, but the expected operating
environment often only allows TCP or HTTP relay paths:

- local Docker VLESS clients,
- frp TCP or HTTP exposure,
- cloudflared-style HTTP tunnels,
- corporate or campus networks where UDP is blocked or unstable.

Directly forwarding those relay paths to `sshd` cannot preserve a live SSH
session after a relay disconnects. A new TCP path creates a new `sshd`
connection. To survive relay churn, MoltSSH server must hold the stable
server-side TCP connection to `sshd`, while MoltSSH client reconnects over
whichever TCP/HTTP path is currently healthy.

## Decision

Make WebSocket resume the only MVP transport because it fits common TCP/HTTP
relay tools and reverse proxies.

`moltssh server` is a plain WebSocket backend in the MVP. TLS is selected per
client path: use `ws://` for trusted direct, LAN, localhost, or already
protected tunnel paths, and `wss://` when a reverse proxy, API gateway, relay,
or local tunnel endpoint terminates TLS in front of `moltssh server`.

The stable user interface is TOML-only:

```bash
moltssh proxy  --config FILE
moltssh server --config FILE
moltssh probe  --config FILE
```

The MVP protocol uses:

- server-generated `session_id`,
- monotonically increasing `epoch` fencing,
- absolute byte offsets per direction,
- ACKs that advance only after downstream writes succeed,
- replay of unacknowledged data after reconnect,
- bounded per-direction buffers,
- exact-offset FIN for half-close propagation.

Raw TCP and QUIC remain future transports. They can be added later behind the
same TOML path model after WebSocket resume is correct.

## Alternatives

### Keep QUIC as the MVP

Rejected for the primary path. QUIC is a good fit for UDP-capable networks, but
it does not match TCP/HTTP-only relay environments. Running QUIC over TCP would
add head-of-line blocking and make failures harder to understand.

### Forward existing TCP relays directly to `sshd`

Rejected. This is operationally simple, but relay disconnects terminate the
server-side SSH TCP connection, so the SSH session cannot resume.

### Build a new MoltSSH relay system

Rejected for MVP. Existing tools already provide TCP/HTTP reachability.
MoltSSH should add the missing resumable byte-stream semantics instead of
owning relay infrastructure.

### Make MoltSSH server terminate TLS itself

Rejected for MVP. TLS termination is already handled well by reverse proxies,
API gateways, relays, and local tunnel endpoints. Keeping `moltssh server` as a
plain backend removes certificate, CA, pinning, and verification config from
the user-facing MVP.

### Support raw TCP or QUIC in the MVP

Rejected for MVP. WebSocket is enough to validate resumable SSH byte streams
through existing TCP/HTTP relay paths. Raw TCP and QUIC are added only after
WebSocket resume and path switching work.

### Support every transport behind a provider abstraction

Rejected for MVP. WebSocket is the only transport. A transport interface is
added only when a second concrete transport is implemented.

## Consequences

- The MVP is more protocol-heavy than the original QUIC-only M0.
- Server process lifetime matters: if `moltssh server` dies, live sessions die.
- Buffer limits and resume timeout are explicit user-facing settings.
- Path switching is application-level: probe, reconnect, resume, and fence old
  epochs.
- The design works with existing TCP/HTTP relays without requiring a new relay
  service.
- TLS policy is expressed by each path endpoint (`ws://` vs `wss://`), not by
  `moltssh server` certificate settings.
- Raw TCP and QUIC can still be useful later, but they are not MVP
  implementation dependencies.
