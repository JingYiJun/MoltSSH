#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
IMAGE=${MOLTSSH_DOCKER_SMOKE_IMAGE:-moltssh-sshd-smoke:local}
NAME=moltssh-sshd-smoke-$$
TMP=$(mktemp -d)
export XDG_CACHE_HOME="$TMP/cache"
SERVER_PID=
RELAY_PIDS=()

cleanup() {
  if [ -n "$SERVER_PID" ]; then
    kill "$SERVER_PID" >/dev/null 2>&1 || true
  fi
  for pid in "${RELAY_PIDS[@]}"; do
    kill "$pid" >/dev/null 2>&1 || true
  done
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  docker rmi "$IMAGE" >/dev/null 2>&1 || true
  rm -rf "$TMP"
}
trap cleanup EXIT

free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

go build -o "$TMP/moltssh" "$ROOT/cmd/moltssh"
ssh-keygen -q -t ed25519 -N "" -C moltssh-docker-smoke -f "$TMP/id_ed25519"
PUB=$(cat "$TMP/id_ed25519.pub")

cat >"$TMP/tcp-relay.py" <<'PY'
#!/usr/bin/env python3
import argparse
import socket
import threading
import time

parser = argparse.ArgumentParser()
parser.add_argument("--listen-host", default="127.0.0.1")
parser.add_argument("--listen-port", type=int, required=True)
parser.add_argument("--upstream-host", default="127.0.0.1")
parser.add_argument("--upstream-port", type=int, required=True)
parser.add_argument("--delay-ms", type=float, default=0)
parser.add_argument("--fail-after", type=float, default=0)
args = parser.parse_args()

delay = args.delay_ms / 1000
stop = threading.Event()
lock = threading.Lock()
sockets = set()


def remember(sock):
    with lock:
        sockets.add(sock)


def forget(sock):
    with lock:
        sockets.discard(sock)


def close_sock(sock):
    try:
        sock.shutdown(socket.SHUT_RDWR)
    except OSError:
        pass
    try:
        sock.close()
    except OSError:
        pass
    forget(sock)


def close_all():
    with lock:
        current = list(sockets)
    for sock in current:
        close_sock(sock)


def forward(src, dst):
    try:
        while not stop.is_set():
            data = src.recv(65536)
            if not data:
                break
            if delay:
                time.sleep(delay)
            dst.sendall(data)
    except OSError:
        pass
    finally:
        close_sock(src)
        close_sock(dst)


def handle(client):
    upstream = None
    try:
        upstream = socket.create_connection((args.upstream_host, args.upstream_port), timeout=5)
        remember(client)
        remember(upstream)
        threads = [
            threading.Thread(target=forward, args=(client, upstream), daemon=True),
            threading.Thread(target=forward, args=(upstream, client), daemon=True),
        ]
        for thread in threads:
            thread.start()
        for thread in threads:
            thread.join()
    except OSError:
        close_sock(client)
        if upstream is not None:
            close_sock(upstream)


listener = socket.socket()
listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
listener.bind((args.listen_host, args.listen_port))
listener.listen()


def fail_later():
    time.sleep(args.fail_after)
    stop.set()
    close_sock(listener)
    close_all()


if args.fail_after > 0:
    threading.Thread(target=fail_later, daemon=True).start()

while not stop.is_set():
    try:
        client, _ = listener.accept()
    except OSError:
        break
    threading.Thread(target=handle, args=(client,), daemon=True).start()
PY

docker build --build-arg PUBKEY="$PUB" -t "$IMAGE" -f - "$ROOT" <<'DOCKERFILE'
FROM nginx:alpine
ARG PUBKEY
RUN apk add --no-cache openssh-server \
    && adduser -D -s /bin/sh test \
    && sed -i 's/^test:[^:]*:/test::/' /etc/shadow \
    && mkdir -p /run/sshd /home/test/.ssh \
    && printf '%s\n' "$PUBKEY" > /home/test/.ssh/authorized_keys \
    && chown -R test:test /home/test/.ssh \
    && chmod 700 /home/test/.ssh \
    && chmod 600 /home/test/.ssh/authorized_keys \
    && ssh-keygen -A
EXPOSE 2222
CMD ["/usr/sbin/sshd", "-D", "-e", "-p", "2222", "-o", "PasswordAuthentication=no"]
DOCKERFILE

docker run -d --rm --name "$NAME" -p 127.0.0.1::2222 "$IMAGE" >/dev/null
SSH_PORT=$(docker port "$NAME" 2222/tcp | sed 's/.*://')

for i in $(seq 1 30); do
  if ssh -F /dev/null -p "$SSH_PORT" -i "$TMP/id_ed25519" \
    -o IdentitiesOnly=yes \
    -o BatchMode=yes \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile="$TMP/known_hosts" \
    test@127.0.0.1 true >/dev/null 2>"$TMP/ssh-ready.err"; then
    break
  fi
  if [ "$i" = 30 ]; then
    docker logs "$NAME" >&2 || true
    cat "$TMP/ssh-ready.err" >&2 || true
    echo "sshd did not become ready" >&2
    exit 1
  fi
  sleep 0.5
done

MOLTSSH_PORT=$(free_port)
FAST_PORT=$(free_port)
SLOW_PORT=$(free_port)
cat >"$TMP/moltssh.toml" <<EOF
schema_version = 1
name = "docker-ssh-multipath"

[server]
listen = "127.0.0.1:${MOLTSSH_PORT}"
http_path = "/moltssh"
connect = "127.0.0.1:${SSH_PORT}"

[resume]
timeout = "10s"
buffer_bytes = 1048576

[probe]
interval = "100ms"
timeout = "2s"
switch_cooldown = "200ms"
active_failure_threshold = 1
candidate_success_threshold = 1
better_rtt_min_delta = "20ms"
better_rtt_ratio = 0.20

[[paths]]
name = "fast-lan"
transport = "ws"
endpoint = "ws://127.0.0.1:${FAST_PORT}/moltssh"
priority = 100
enabled = true

[[paths]]
name = "slow-relay"
transport = "ws"
endpoint = "ws://127.0.0.1:${SLOW_PORT}/moltssh"
priority = 50
enabled = true
EOF

"$TMP/moltssh" server --config "$TMP/moltssh.toml" >"$TMP/moltssh-server.log" 2>&1 &
SERVER_PID=$!

python3 "$TMP/tcp-relay.py" \
  --listen-port "$FAST_PORT" \
  --upstream-port "$MOLTSSH_PORT" \
  --delay-ms 10 \
  --fail-after 2.0 \
  >"$TMP/fast-relay.log" 2>&1 &
RELAY_PIDS+=("$!")

python3 "$TMP/tcp-relay.py" \
  --listen-port "$SLOW_PORT" \
  --upstream-port "$MOLTSSH_PORT" \
  --delay-ms 120 \
  >"$TMP/slow-relay.log" 2>&1 &
RELAY_PIDS+=("$!")

for i in $(seq 1 40); do
  if "$TMP/moltssh" probe --config "$TMP/moltssh.toml" >"$TMP/moltssh-probe.log" 2>&1 \
    && grep -q "path=fast-lan status=ok" "$TMP/moltssh-probe.log" \
    && grep -q "path=slow-relay status=ok" "$TMP/moltssh-probe.log"; then
    break
  fi
  if [ "$i" = 40 ]; then
    cat "$TMP/moltssh-server.log" >&2 || true
    cat "$TMP/moltssh-probe.log" >&2 || true
    echo "moltssh server did not become ready" >&2
    exit 1
  fi
  sleep 0.25
done

set +e
OUT=$(ssh -F /dev/null -i "$TMP/id_ed25519" \
  -o IdentitiesOnly=yes \
  -o BatchMode=yes \
  -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile="$TMP/known_hosts" \
  -o ProxyCommand="$TMP/moltssh proxy --config $TMP/moltssh.toml" \
  test@ignored 'i=1; while [ "$i" -le 50 ]; do printf "tick-%02d\n" "$i"; i=$((i+1)); sleep 0.12; done' \
  2>"$TMP/ssh.err")
SSH_STATUS=$?
set -e

if [ "$SSH_STATUS" -ne 0 ]; then
  cat "$TMP/moltssh-probe.log" >&2 || true
  cat "$TMP/moltssh-server.log" >&2 || true
  cat "$TMP/fast-relay.log" >&2 || true
  cat "$TMP/slow-relay.log" >&2 || true
  cat "$TMP/ssh.err" >&2 || true
  echo "ssh command failed with status $SSH_STATUS" >&2
  exit "$SSH_STATUS"
fi

EXPECTED=$(python3 - <<'PY'
print("\n".join(f"tick-{i:02d}" for i in range(1, 51)))
PY
)

if [ "$OUT" != "$EXPECTED" ]; then
  echo "unexpected ssh output:" >&2
  printf '%s\n' "$OUT" >&2
  echo "expected:" >&2
  printf '%s\n' "$EXPECTED" >&2
  exit 1
fi

if ! grep -q "proxy active path=fast-lan" "$TMP/ssh.err"; then
  cat "$TMP/ssh.err" >&2
  echo "fast-lan path was not activated" >&2
  exit 1
fi

if ! grep -q "proxy active path=slow-relay" "$TMP/ssh.err"; then
  cat "$TMP/ssh.err" >&2
  echo "slow-relay path was not activated after fast-lan failed" >&2
  exit 1
fi

for key in dns tcp tls websocket_upgrade probe_rtt total failed_phase; do
  if ! grep -q "${key}=" "$TMP/moltssh-probe.log"; then
    cat "$TMP/moltssh-probe.log" >&2
    echo "probe output is missing ${key}" >&2
    exit 1
  fi
done

if ! grep -Eq 'event=proxy_dial .*dns=.*tcp=.*tls=.*websocket_upgrade=.*moltssh_hello=.*probe_rtt=.*total=' "$TMP/ssh.err"; then
  cat "$TMP/ssh.err" >&2
  echo "proxy dial phase timing record was not emitted" >&2
  exit 1
fi

python3 - "$XDG_CACHE_HOME" <<'PY'
import json
import pathlib
import sys

files = list((pathlib.Path(sys.argv[1]) / "moltssh" / "path-state").glob("*.json"))
if len(files) != 1:
    raise SystemExit(f"expected one path-state file, found {len(files)}")
record = json.loads(files[0].read_text())
if record != {"version": 1, "path": "slow-relay"}:
    raise SystemExit(f"unexpected path-state record: {record!r}")
PY

WARM_OUT=$(ssh -F /dev/null -i "$TMP/id_ed25519" \
  -o IdentitiesOnly=yes \
  -o BatchMode=yes \
  -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile="$TMP/known_hosts" \
  -o ProxyCommand="$TMP/moltssh proxy --config $TMP/moltssh.toml" \
  test@ignored 'printf warm-lkg-ok' \
  2>"$TMP/warm-ssh.err")

if [ "$WARM_OUT" != "warm-lkg-ok" ]; then
  cat "$TMP/warm-ssh.err" >&2 || true
  echo "warm LKG SSH command returned unexpected output: $WARM_OUT" >&2
  exit 1
fi

if ! grep -Eq 'event=proxy_dial path=slow-relay status=ok .*probe_rtt=0s' "$TMP/warm-ssh.err"; then
  cat "$TMP/warm-ssh.err" >&2 || true
  echo "warm startup did not direct-dial the saved slow-relay path" >&2
  exit 1
fi

cat "$TMP/moltssh-probe.log"
grep -E "proxy active path=|event=proxy_dial" "$TMP/ssh.err"
grep -E "proxy active path=|event=proxy_dial" "$TMP/warm-ssh.err"
printf '%s\n' "docker-ssh-multipath-ok"
