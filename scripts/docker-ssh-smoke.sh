#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
IMAGE=${MOLTSSH_DOCKER_SMOKE_IMAGE:-moltssh-sshd-smoke:local}
NAME=moltssh-sshd-smoke-$$
TMP=$(mktemp -d)

cleanup() {
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
  if ssh -p "$SSH_PORT" -i "$TMP/id_ed25519" \
    -o IdentitiesOnly=yes \
    -o BatchMode=yes \
    -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile="$TMP/known_hosts" \
    test@127.0.0.1 true >/dev/null 2>&1; then
    break
  fi
  if [ "$i" = 30 ]; then
    docker logs "$NAME" >&2 || true
    echo "sshd did not become ready" >&2
    exit 1
  fi
  sleep 0.5
done

MOLTSSH_PORT=$(free_port)
cat >"$TMP/moltssh.toml" <<EOF
schema_version = 1
name = "docker-ssh"

[server]
listen = "127.0.0.1:${MOLTSSH_PORT}"
http_path = "/moltssh"
connect = "127.0.0.1:${SSH_PORT}"

[resume]
timeout = "10s"
buffer_bytes = 1048576

[probe]
interval = "100ms"
timeout = "1s"
switch_cooldown = "200ms"
active_failure_threshold = 2
candidate_success_threshold = 1
better_rtt_min_delta = "1ms"
better_rtt_ratio = 0.25

[[paths]]
name = "docker-local"
transport = "ws"
endpoint = "ws://127.0.0.1:${MOLTSSH_PORT}/moltssh"
priority = 100
enabled = true
EOF

"$TMP/moltssh" server --config "$TMP/moltssh.toml" >"$TMP/moltssh-server.log" 2>&1 &
SERVER_PID=$!
trap 'kill "$SERVER_PID" >/dev/null 2>&1 || true; cleanup' EXIT

for i in $(seq 1 40); do
  if "$TMP/moltssh" probe --config "$TMP/moltssh.toml" >"$TMP/moltssh-probe.log" 2>&1; then
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

OUT=$(ssh -i "$TMP/id_ed25519" \
  -o IdentitiesOnly=yes \
  -o BatchMode=yes \
  -o StrictHostKeyChecking=no \
  -o UserKnownHostsFile="$TMP/known_hosts" \
  -o ProxyCommand="$TMP/moltssh proxy --config $TMP/moltssh.toml" \
  test@ignored 'printf docker-ssh-ok')

if [ "$OUT" != "docker-ssh-ok" ]; then
  echo "unexpected ssh output: $OUT" >&2
  exit 1
fi

cat "$TMP/moltssh-probe.log"
printf '%s\n' "$OUT"
