# MoltSSH MVP

## Goal

MoltSSH keeps an OpenSSH `ProxyCommand` byte stream alive when the active
network path is slow, flaky, or disconnected. The MVP is TCP/WebSocket-first:
existing TCP/HTTP relay tools provide reachability, and MoltSSH provides
session resume, path probing, and path switching.

UDP/QUIC remains an optional auxiliary transport after the TCP/WebSocket
protocol is proven.

## Non-goals

- Do not build a new relay system in the MVP.
- Do not connect relay tools directly to `sshd`; MoltSSH server must hold the
  stable TCP connection to `sshd`.
- Do not support TCP-only path switching without application-level resume.
- Do not add provider plugins or transport registries before a second transport
  is implemented.
- Do not survive a `moltssh server` restart or a broken server-to-`sshd`
  connection.

## User interface

The CLI only accepts a TOML config path. Runtime choices belong in TOML, not in
flags.

```bash
moltssh proxy  --config ~/.config/moltssh/lab.toml
moltssh server --config /etc/moltssh/lab.toml
moltssh probe  --config ~/.config/moltssh/lab.toml
```

OpenSSH config:

```sshconfig
Host lab-box
  HostName ignored
  User jingyijun
  ProxyCommand moltssh proxy --config ~/.config/moltssh/lab.toml
```

`proxy` opens or resumes a session. `server` listens for client sessions and
bridges each accepted session to one server-side TCP target. `probe` measures
all enabled client paths and prints their current health and RTT.

## TOML config

The MVP uses one TOML schema for client and server. Client-only fields are
ignored by `server`; server-only fields are ignored by `proxy` and `probe`.
MoltSSH fills stable runtime defaults before validation. Deployment-specific
addresses and names stay explicit.

```toml
schema_version = 1
name = "lab-box"

[server]
listen = "127.0.0.1:8080"
connect = "127.0.0.1:22"

[[paths]]
name = "direct"
endpoint = "ws://192.168.1.20:8080/moltssh"
priority = 100

[[paths]]
name = "frp"
endpoint = "wss://frp.example.com/moltssh"
priority = 70

[[paths]]
name = "vless-local"
endpoint = "ws://127.0.0.1:24433/moltssh"
priority = 50
```

Field behavior:

| Field | Applies to | Default | Rule |
|---|---|---:|---|
| `schema_version` | all | required | Must be `1`; unknown versions fail fast. |
| `name` | all | required | Human-readable connection name. It is sent in `hello`, used in logs, and does not affect routing. |
| `server.listen` | server | required | Local TCP address the MoltSSH server binds. Reverse proxies, frp, cloudflared, or VLESS can forward to this address. |
| `server.http_path` | server | `"/moltssh"` | Plain WebSocket request path. Server accepts this path. Client `ws://` and `wss://` endpoints carry their own path in `paths[].endpoint`. |
| `server.connect` | server | required | The single server-side TCP target, usually `127.0.0.1:22`. Every accepted MoltSSH session gets one TCP connection to this target. |
| `resume.timeout` | proxy/server | `"60s"` | Maximum time a live session may stay disconnected. Server closes the TCP connection to `server.connect` after this timeout. |
| `resume.buffer_bytes` | proxy/server | `33554432` | Per-direction in-memory unacknowledged byte buffer limit. When full, MoltSSH stops reading upstream until ACKs free space. |
| `probe.interval` | proxy/probe | `"3s"` | Time between health probes for enabled inactive paths. |
| `probe.timeout` | proxy/probe | `"2s"` | Per-probe deadline. A path that misses this deadline counts as one failed probe. |
| `probe.switch_cooldown` | proxy | `"10s"` | Minimum time between latency-driven path switches. Failure-driven switches use the latest probe result immediately. |
| `probe.active_failure_threshold` | proxy | `2` | Consecutive failed in-session heartbeats on the active path before failover starts. |
| `probe.candidate_success_threshold` | proxy | `3` | Consecutive successful probes required before an inactive path may become active. |
| `probe.better_rtt_min_delta` | proxy | `"30ms"` | Candidate must be at least this many milliseconds faster than the active path before a latency-driven switch. |
| `probe.better_rtt_ratio` | proxy | `0.25` | Candidate must also be at least this fraction faster than the active path. `0.25` means 25% faster. |
| `paths[].name` | proxy/probe | required | Unique path name used in logs, probe output, and active-path decisions. |
| `paths[].transport` | proxy/probe | `"ws"` | WebSocket transport for the MVP. Unknown values fail fast. |
| `paths[].endpoint` | proxy/probe | required | Full WebSocket endpoint URL. `ws://` is for trusted direct/local paths; `wss://` is for TLS-terminated relay paths. Other schemes fail fast in the MVP. |
| `paths[].priority` | proxy/probe | `0` | Tie-breaker when healthy paths have effectively equal RTT. Higher wins. |
| `paths[].enabled` | proxy/probe | `true` | Disabled paths stay in config while enabled paths take traffic and probes. |

Duration fields use Go duration strings such as `500ms`, `3s`, or `1m`.
Unknown TOML keys fail config validation.

Command-specific validation:

- `server` requires `schema_version`, `name`, `server.listen`, and
  `server.connect`.
- `proxy` requires `schema_version`, `name`, and at least one enabled
  `[[paths]]` entry.
- `probe` requires `schema_version`, `name`, and at least one enabled
  `[[paths]]` entry.

There is no `session_dir` in the MVP. Live sessions, offsets, epochs, and
unacknowledged byte buffers are process-memory state only. MoltSSH does not
persist SSH payload bytes or live connection metadata to disk. The only
persisted client hint is an advisory last-known-good path-name cache under the
user cache directory. It is keyed by a SHA-256 hash of the canonical config
path and contains exactly `{"version":1,"path":"<name>"}`. Directory mode is
`0700`, file mode is `0600`, and replacement is atomic. Cache read/write
failure is non-fatal, and deleting the cache is safe.

## Transport plan

MVP order:

1. WebSocket (`ws://` or `wss://`).

Raw TCP and QUIC are out of the MVP and can be added later only after WebSocket
resume and path switching are correct.

The active data path is single-writer. Other paths may be probed, but they do
not carry SSH bytes until they become active.

TLS is a deployment concern, not a MoltSSH server concern:

- `moltssh server` listens as a plain WebSocket backend.
- Use `ws://` for trusted direct, LAN, localhost, or already-protected tunnel
  paths.
- Use `wss://` when the path crosses an untrusted relay or public endpoint.
- For `wss://`, TLS is terminated by the reverse proxy, API gateway, relay, or
  local tunnel endpoint in front of `moltssh server`.
- MoltSSH has no certificate, CA, pinning, or TLS verification fields in the
  MVP.
- MoltSSH has no application-layer authentication in the MVP. Deploy it only
  behind protected local, private-network, TLS-terminated, or externally
  authenticated access.

## Congestion control and backpressure

MoltSSH does not implement a congestion control algorithm in the MVP.

For `ws://` and `wss://`, congestion control, retransmission, and RTO are
handled by the operating system TCP stack. MoltSSH only applies
application-level backpressure: when a per-direction unacknowledged buffer
reaches `resume.buffer_bytes`, MoltSSH stops reading from the upstream side
until ACKs free buffer space.

Probe RTT is used only for path selection. It must not pace data frames in the
MVP.

## TCP/WebSocket control protocol

The protocol runs over one reliable bidirectional WebSocket connection. Each
MoltSSH frame is one binary WebSocket message.

Clients and servers use this WebSocket subprotocol:

```text
Sec-WebSocket-Protocol: moltssh.v1
```

The server rejects WebSocket upgrades that do not request `moltssh.v1`.

Frame envelope:

```text
uint32 json_header_len_be
uint32 payload_len_be
json_header
payload
```

The JSON header is UTF-8 and must contain `type`. `payload_len_be` is zero for
frames without a payload.

Limits:

- `json_header_len_be` must be at most 65536 bytes.
- `payload_len_be` for `data` must be at most 1048576 bytes.
- `payload_len_be` must be zero for non-`data` frames.
- The receiver rejects oversized or truncated frames with `error`.

One binary WebSocket message contains exactly one full MoltSSH envelope.

Frame types:

| Type | Direction | Required fields | Meaning |
|---|---|---|---|
| `hello` | client -> server | `version`, `name`, `resume` | Starts a new session or asks to resume. |
| `accept` | server -> client | `session_id`, `epoch`, `client_to_server_rx`, `server_to_client_rx` | Accepts the active connection. |
| `data` | both | `session_id`, `epoch`, `direction`, `offset` | Carries SSH bytes. |
| `ack` | both | `session_id`, `epoch`, `direction`, `received_offset` | Acknowledges bytes written downstream. |
| `fin` | both | `session_id`, `epoch`, `direction`, `offset` | Propagates half-close at an exact offset. |
| `ping` | both | `nonce`, `sent_at_unix_nano` | Measures application RTT. |
| `pong` | both | `nonce`, `sent_at_unix_nano` | Replies to `ping`. |
| `close` | both | `session_id`, `epoch`, `code`, `message` | Graceful terminal close. |
| `error` | both | `code`, `message` | Rejects malformed frames or invalid protocol state. |

`direction` is `client_to_server` for SSH stdin bytes and `server_to_client`
for bytes read from the server-side TCP target.

The server must not use `name` for authentication or routing.
The server may accept multiple live sessions. Each session owns one
server-side TCP connection to `server.connect` and independent in-memory
buffers.

New session:

```json
{"type":"hello","version":1,"name":"lab-box","resume":false}
```

Resume:

```json
{
  "type": "hello",
  "version": 1,
  "name": "lab-box",
  "resume": true,
  "session_id": "base64url-no-padding-random-32-bytes",
  "client_to_server_rx": 1048576,
  "server_to_client_rx": 2097152
}
```

Accept:

```json
{
  "type": "accept",
  "session_id": "base64url-no-padding-random-32-bytes",
  "epoch": 7,
  "client_to_server_rx": 1048576,
  "server_to_client_rx": 2097152
}
```

Resume rules:

- `session_id` is generated by the server from 32 cryptographically secure
  random bytes and encoded with URL-safe base64 without padding.
- On a new `hello` with `resume = false`, the server dials `server.connect`
  before sending `accept`. If dialing fails, the server sends `error` and
  closes the WebSocket.
- `epoch` is a fencing token. A higher accepted epoch invalidates older active
  connections for the same session.
- After accepting a higher epoch, both sides stop reading `data` frames from
  older epochs and close those connections. Stale `ack`, `close`, or `error`
  frames may be logged but must not change session state.
- Each direction uses absolute byte offsets starting at zero.
- `client_to_server_rx` is the highest contiguous offset the server has
  successfully written to `server.connect`.
- `server_to_client_rx` is the highest contiguous offset the client has
  successfully written to stdout.
- All receive offsets are exclusive. Offset `100` means bytes `[0,100)` are
  written downstream.
- `data.offset` is the absolute offset of the first payload byte.
- A sender may send `data` only at the next unsent or unacknowledged offset for
  that direction.
- A receiver with current receive offset `rx` accepts `data` only when
  `data.offset == rx`. If `data.offset > rx`, the receiver sends
  `protocol_error`. If `data.offset < rx`, the receiver may ignore the frame
  only when the whole payload is already below `rx`; partial overlap is
  `protocol_error`.
- `ack.received_offset` advances only after bytes are successfully written to
  the downstream writer: `sshd` on the server side, stdout on the client side.
- `ack.received_offset` is exclusive.
- Receivers should send `ack` after each successful downstream write and must
  send `ack` before close, reconnect handoff, or buffer backpressure.
- On reconnect, each side resends unacknowledged bytes after the peer's reported
  receive offset.
- `fin.offset` is acknowledged and replayed like data. It must not overtake
  missing data.
- `fin.offset` is exclusive: it is the first byte after the stream in that
  direction.
- If either per-direction buffer reaches `resume.buffer_bytes`, stop reading
  upstream until ACKs free space.
- If disconnected longer than `resume.timeout`, close the server-side TCP
  connection and reject later resumes.
- The `resume.timeout` timer starts when the active WebSocket is lost or closed
  without a terminal `close` frame. It resets after an accepted resume.
- Only a MoltSSH `close` frame is a terminal session close. A bare WebSocket
  close without a MoltSSH `close` frame is treated as a disconnect and may be
  resumed until `resume.timeout`.
- `fin` closes one byte direction at an exact offset. `close` terminates the
  whole session and releases the server-side TCP connection.
- `fin` follows the same offset checks as `data`: it is accepted only at the
  current receive offset for that direction, and a duplicate already below the
  current receive offset may be ignored.
- Malformed offsets, stale epochs, or unknown sessions terminate the connection
  with `error`.

`ping` and `pong` are path-level frames. They may be sent before `hello`, do
not require `session_id` or `epoch`, and must not change session state. The
active path sends heartbeat `ping` on the existing session WebSocket. The
normal session receive loop remains the sole reader and dispatches the matching
`pong`; heartbeat code must never add a competing WebSocket reader.

Close codes:

| Code | Meaning |
|---|---|
| `normal` | Clean session shutdown. |
| `protocol_error` | Malformed frame, bad offset, stale epoch, or invalid state. |
| `resume_timeout` | Session stayed disconnected longer than `resume.timeout`. |
| `target_closed` | Server-side TCP target closed. |
| `target_dial_failed` | Server failed to dial `server.connect`. |

## Path selection

The active path changes only when doing so is clearly useful:

- Switch immediately when the active path fails
  `probe.active_failure_threshold` times.
- Prefer a candidate only after `probe.candidate_success_threshold` consecutive
  successful probes.
- Switch for latency only when the candidate RTT is at least
  `probe.better_rtt_min_delta` faster and at least
  `probe.better_rtt_ratio` better than the active path.
- After a switch, wait `probe.switch_cooldown` before switching again unless the
  active path fails.
- If RTT is effectively equal, choose the higher `paths[].priority`.

This avoids flapping between relays for small RTT differences.

On cold startup, `proxy` probes enabled paths concurrently with at most eight
workers. Results are ranked by success, RTT, priority, and declaration order.
The selected successful probe WebSocket is promoted on the same connection:
`proxy` sends `hello` after the matching `pong`, rather than doing a second
DNS/TCP/TLS/WebSocket dial.

After an accepted activation, `proxy` saves only that path name as the
last-known-good hint. On warm startup it direct-dials that path first while
probing the alternatives in the background. Success returns without waiting
for background probes; those probes are cancelled, drained, and closed. A
failed, stale, disabled, malformed, or unavailable hint falls back to the
ranked concurrent candidates. Probe-only success never updates the hint.

For steady-state selection, the active path is measured by in-session
heartbeat, so periodic checks add no DNS lookup, TCP connection, TLS handshake,
or WebSocket upgrade for the active path. Only inactive paths are probed. To
switch, `proxy` promotes the current successful inactive probe by sending a
resume `hello` with current receive offsets on that same WebSocket, waits for
`accept` with a higher `epoch`, marks it active, then closes the old WebSocket.

During reconnect or path switch, `proxy` keeps OpenSSH stdin/stdout open and
blocks reads or writes as needed. It exits non-zero only after
`resume.timeout`, a terminal `close`, or a terminal `error`.

`moltssh probe --config ...` prints one line per enabled path:

```text
path=<name> status=<ok|fail> dns=<duration> tcp=<duration> tls=<duration> websocket_upgrade=<duration> probe_rtt=<duration> total=<duration> failed_phase=<phase> endpoint=<url> error=<message>
```

All duration fields use Go `time.Duration.String()`. Output remains in TOML
declaration order even though probes run concurrently. `failed_phase` and
`error` are empty on success. Every field is always printed. Endpoint and
error output must redact credentials and secret query parameters.

Probe uses `ping`/`pong` only. It must not create a session and must not dial
`server.connect`.

## Milestones

### M0: TOML and WebSocket loop

- `moltssh proxy --config ...` and `moltssh server --config ...`.
- Parse and validate the TOML schema.
- WebSocket transport can bridge stdin/stdout to `server.connect`.
- M0 still uses `Sec-WebSocket-Protocol: moltssh.v1`, the frame envelope, and
  `hello`/`accept`.
- M0 accepts only `hello` with `resume = false`, returns `epoch = 1`, and
  rejects `resume = true` with `protocol_error`.
- `moltssh probe --config ...` prints path health and application RTT.

### M1: Session resume

- Server keeps one TCP connection to `server.connect` across client reconnects.
- `session_id`, `epoch`, offsets, ACKs, FIN, and bounded buffers work.
- Killing the active WebSocket connection and reconnecting resumes the SSH byte
  stream without duplication or reordering.

### M2: Multi-path switching

- Probe all enabled paths.
- Fail over from a broken path to a healthy path.
- Switch to a lower-latency path only after hysteresis thresholds are met.

### M3: Operations

- Reject stale epochs, unknown sessions, and bad offsets.
- A failed resume attempt uses capped exponential backoff with full jitter:
  immediate first attempt, `200ms` base, `5s` cap, clipped to the remaining
  `resume.timeout` budget. A successful resume resets the attempt counter.
- Each formal dial attempt emits exactly one `event=proxy_dial` record with
  `path`, `status`, `failed_phase`, `dns`, `tcp`, `tls`,
  `websocket_upgrade`, `moltssh_hello`, `probe_rtt`, `total`, and `error`.
- Logs include path, session, epoch, close reason, and RTT where applicable,
  but never private keys, credentials, relay URLs with embedded secrets, or
  SSH payload bytes.
