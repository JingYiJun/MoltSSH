<a id="readme-top"></a>

# MoltSSH

[English](README.md) | [中文](README.zh.md)

[![CI](https://github.com/JingYiJun/MoltSSH/actions/workflows/ci.yml/badge.svg)](https://github.com/JingYiJun/MoltSSH/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/JingYiJun/MoltSSH)](https://github.com/JingYiJun/MoltSSH/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

MoltSSH is a resumable OpenSSH `ProxyCommand` over WebSocket.

The current MVP uses WebSocket transport. Existing relay tools provide
reachability; MoltSSH provides session resume, path probing, and path
switching.

[View Releases](https://github.com/JingYiJun/MoltSSH/releases) ·
[Report Bug](https://github.com/JingYiJun/MoltSSH/issues/new?template=bug_report.yml) ·
[Request Feature](https://github.com/JingYiJun/MoltSSH/issues/new?template=feature_request.yml)

## Table of Contents

1. [About The Project](#about-the-project)
2. [Built With](#built-with)
3. [Getting Started](#getting-started)
4. [Usage](#usage)
5. [Configuration](#configuration)
6. [Roadmap](#roadmap)
7. [Contributing](#contributing)
8. [License](#license)
9. [Contact](#contact)
10. [Acknowledgments](#acknowledgments)

## About The Project

MoltSSH keeps the server-side TCP connection to `sshd` stable while the client
reconnects through the healthiest configured WebSocket path. This lets an
OpenSSH session continue through client-side network churn, relay restarts, and
path changes that preserve the MoltSSH server process.

Current capabilities:

- OpenSSH `ProxyCommand` compatibility.
- TOML based `proxy`, `server`, and `probe` commands.
- WebSocket subprotocol `moltssh.v1` with explicit binary frames.
- Server-side TCP bridge to a single configured target such as `127.0.0.1:22`.
- In-memory resume state with session IDs, epochs, ACKs, FIN, offsets, and
  replay buffers.
- Probe driven path selection with RTT, failure threshold, success threshold,
  and switch cooldown settings.
- GitHub Actions CI, release binaries, issue templates, and Docker SSH smoke
  coverage.

See [docs/mvp.md](docs/mvp.md) for the protocol plan and
[ADR 0001](docs/adr/0001-tcp-ws-primary-quic-auxiliary.md) for the transport
decision.

<p align="right">(<a href="#readme-top">back to top</a>)</p>

## Built With

- [Go](https://go.dev/)
- [golang.org/x/net/websocket](https://pkg.go.dev/golang.org/x/net/websocket)
- [OpenSSH](https://www.openssh.com/)
- [Docker](https://www.docker.com/) for the repository smoke test

<p align="right">(<a href="#readme-top">back to top</a>)</p>

## Getting Started

### Prerequisites

- Go 1.26.5+
- OpenSSH client
- Docker for `scripts/docker-ssh-smoke.sh`

### Installation

Download a release binary from
[GitHub Releases](https://github.com/JingYiJun/MoltSSH/releases).

Release assets use this naming pattern:

```text
moltssh_<version>_<os>_<arch>
moltssh_<version>_windows_amd64.exe
SHA256SUMS
```

Build from source:

```bash
git clone https://github.com/JingYiJun/MoltSSH.git
cd MoltSSH
go build -o moltssh ./cmd/moltssh
```

Run local checks:

```bash
go test ./...
go test -race ./...
scripts/docker-ssh-smoke.sh
```

For a writable Go cache path:

```bash
GOCACHE=/tmp/moltssh-go-build go test ./...
```

<p align="right">(<a href="#readme-top">back to top</a>)</p>

## Usage

Start a MoltSSH server beside the target `sshd`:

```bash
moltssh server --config /etc/moltssh/example.toml
```

Probe configured client paths:

```bash
moltssh probe --config ~/.config/moltssh/example.toml
```

Use MoltSSH as an OpenSSH `ProxyCommand`:

```sshconfig
Host example-host
  HostName example-host
  User example-user
  ProxyCommand moltssh proxy --config ~/.config/moltssh/example.toml
```

Open an SSH session:

```bash
ssh example-host
```

<p align="right">(<a href="#readme-top">back to top</a>)</p>

## Configuration

MoltSSH uses one TOML schema for client and server commands:

```toml
schema_version = 1
name = "example-host"

[server]
listen = "127.0.0.1:8080"
http_path = "/moltssh"
connect = "127.0.0.1:22"

[resume]
timeout = "60s"
buffer_bytes = 33554432

[probe]
interval = "3s"
timeout = "2s"
switch_cooldown = "10s"
active_failure_threshold = 2
candidate_success_threshold = 3
better_rtt_min_delta = "30ms"
better_rtt_ratio = 0.25

[[paths]]
name = "direct"
transport = "ws"
endpoint = "ws://127.0.0.1:8080/moltssh"
priority = 100
enabled = true
```

Deployment guidance:

- Use `ws://` for trusted localhost, LAN, and already protected tunnel paths.
- Use `wss://` through a reverse proxy, relay, gateway, or local tunnel endpoint
  that terminates TLS.
- Place the MoltSSH server behind a protected access layer for public relay
  paths.
- Keep private keys, credentials, certificates, tokens, and machine-specific
  host details in local deployment files.

<p align="right">(<a href="#readme-top">back to top</a>)</p>

## Roadmap

- [x] WebSocket transport.
- [x] TOML config validation.
- [x] OpenSSH `ProxyCommand` bridge.
- [x] Session resume with replay buffers.
- [x] Probe command and path switching.
- [x] Docker SSH smoke test.
- [x] GitHub CI and release binaries.
- [ ] More protocol conformance tests.
- [ ] Operational examples for common relay tools.
- [ ] QUIC exploration after WebSocket resume and path switching mature.

See the [open issues](https://github.com/JingYiJun/MoltSSH/issues) for active
work items.

<p align="right">(<a href="#readme-top">back to top</a>)</p>

## Contributing

Contributions are welcome through focused issues and pull requests.

1. Fork the project.
2. Create a feature branch such as `feature/ws-probe-output`.
3. Commit with Conventional Commits, for example
   `feat: improve probe output`.
4. Run `go test ./...`.
5. Open a pull request with scope, testing, and protocol/security notes.

<p align="right">(<a href="#readme-top">back to top</a>)</p>

## License

Distributed under the MIT License. See [LICENSE](LICENSE) for more information.

<p align="right">(<a href="#readme-top">back to top</a>)</p>

## Contact

Project Link: [https://github.com/JingYiJun/MoltSSH](https://github.com/JingYiJun/MoltSSH)

<p align="right">(<a href="#readme-top">back to top</a>)</p>

## Acknowledgments

- [Best-README-Template](https://github.com/othneildrew/Best-README-Template)
  for the README structure.
- [OpenSSH](https://www.openssh.com/) for the integration target.
- [Go](https://go.dev/) for the implementation toolchain.

<p align="right">(<a href="#readme-top">back to top</a>)</p>
