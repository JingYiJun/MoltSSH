<a id="readme-top"></a>

# MoltSSH

[English](README.md) | [中文](README.zh.md)

[![CI](https://github.com/JingYiJun/MoltSSH/actions/workflows/ci.yml/badge.svg)](https://github.com/JingYiJun/MoltSSH/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/JingYiJun/MoltSSH)](https://github.com/JingYiJun/MoltSSH/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

MoltSSH 是基于 WebSocket 的可恢复 OpenSSH `ProxyCommand`。

当前 MVP 使用 WebSocket transport。已有 relay 工具提供可达性；MoltSSH
提供 session resume、path probing 和 path switching。

[查看 Releases](https://github.com/JingYiJun/MoltSSH/releases) ·
[报告 Bug](https://github.com/JingYiJun/MoltSSH/issues/new?template=bug_report.yml) ·
[请求 Feature](https://github.com/JingYiJun/MoltSSH/issues/new?template=feature_request.yml)

## 目录

1. [项目介绍](#项目介绍)
2. [技术栈](#技术栈)
3. [开始使用](#开始使用)
4. [用法](#用法)
5. [配置](#配置)
6. [路线图](#路线图)
7. [贡献](#贡献)
8. [许可证](#许可证)
9. [联系](#联系)
10. [致谢](#致谢)

## 项目介绍

MoltSSH 在客户端通过最健康的 WebSocket path 重新连接时，保持 server 侧到
`sshd` 的 TCP connection 稳定。这样，OpenSSH session 可以穿过客户端网络波动、
relay 重启和 path 变化，并继续复用仍在运行的 MoltSSH server 进程。

当前能力：

- 兼容 OpenSSH `ProxyCommand`。
- 基于 TOML 的 `proxy`、`server` 和 `probe` 命令配置。
- 使用 WebSocket subprotocol `moltssh.v1` 和显式 binary frames。
- Server 侧 TCP bridge 指向单个配置目标，例如 `127.0.0.1:22`。
- 内存态 resume state，包含 session ID、epoch、ACK、FIN、offset 和 replay buffer。
- 由 probe 驱动的 path selection，包含 RTT、failure threshold、success threshold
  和 switch cooldown 设置。
- GitHub Actions CI、release binaries、issue templates 和 Docker SSH smoke 覆盖。

协议计划见 [docs/mvp.md](docs/mvp.md)，transport 决策见
[ADR 0001](docs/adr/0001-tcp-ws-primary-quic-auxiliary.md)。

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>

## 技术栈

- [Go](https://go.dev/)
- [golang.org/x/net/websocket](https://pkg.go.dev/golang.org/x/net/websocket)
- [OpenSSH](https://www.openssh.com/)
- [Docker](https://www.docker.com/) 用于仓库 smoke test

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>

## 开始使用

### 前置条件

- Go 1.26.5+
- OpenSSH client
- Docker，用于 `scripts/docker-ssh-smoke.sh`

### 安装

从 [GitHub Releases](https://github.com/JingYiJun/MoltSSH/releases) 下载 release binary。

Release assets 使用以下命名格式：

```text
moltssh_<version>_<os>_<arch>
moltssh_<version>_windows_amd64.exe
SHA256SUMS
```

从源码构建：

```bash
git clone https://github.com/JingYiJun/MoltSSH.git
cd MoltSSH
go build -o moltssh ./cmd/moltssh
```

运行本地检查：

```bash
go test ./...
go test -race ./...
scripts/docker-ssh-smoke.sh
```

指定可写 Go cache 路径：

```bash
GOCACHE=/tmp/moltssh-go-build go test ./...
```

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>

## 用法

在目标 `sshd` 旁启动 MoltSSH server：

```bash
moltssh server --config /etc/moltssh/example.toml
```

Probe 已配置的 client paths：

```bash
moltssh probe --config ~/.config/moltssh/example.toml
```

把 MoltSSH 作为 OpenSSH `ProxyCommand` 使用：

```sshconfig
Host example-host
  HostName example-host
  User example-user
  ProxyCommand moltssh proxy --config ~/.config/moltssh/example.toml
```

打开 SSH session：

```bash
ssh example-host
```

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>

## 配置

MoltSSH 对 client 和 server 命令使用同一套 TOML schema：

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

部署建议：

- 将 `ws://` 用于可信 localhost、LAN 和已有保护的 tunnel path。
- 将 `wss://` 用于通过 reverse proxy、relay、gateway 或 local tunnel endpoint
  终止 TLS 的路径。
- 将 MoltSSH server 放在 public relay path 的受保护访问层之后。
- 将私钥、凭证、证书、token 和主机细节保存在本地部署文件中。

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>

## 路线图

- [x] WebSocket transport。
- [x] TOML config validation。
- [x] OpenSSH `ProxyCommand` bridge。
- [x] Session resume with replay buffers。
- [x] Probe command and path switching。
- [x] Docker SSH smoke test。
- [x] GitHub CI and release binaries。
- [ ] 更多 protocol conformance tests。
- [ ] 常见 relay tools 的运行示例。
- [ ] WebSocket resume 和 path switching 成熟后探索 QUIC。

查看 [open issues](https://github.com/JingYiJun/MoltSSH/issues) 获取当前工作项。

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>

## 贡献

欢迎通过聚焦的 issue 和 pull request 参与贡献。

1. Fork 项目。
2. 创建 feature branch，例如 `feature/ws-probe-output`。
3. 使用 Conventional Commits 提交，例如 `feat: improve probe output`。
4. 运行 `go test ./...`。
5. 创建 pull request，并写清 scope、testing、protocol/security notes。

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>

## 许可证

使用 MIT License 发布。更多信息见 [LICENSE](LICENSE)。

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>

## 联系

Project Link: [https://github.com/JingYiJun/MoltSSH](https://github.com/JingYiJun/MoltSSH)

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>

## 致谢

- [Best-README-Template](https://github.com/othneildrew/Best-README-Template)
  提供 README 结构。
- [OpenSSH](https://www.openssh.com/) 提供集成目标。
- [Go](https://go.dev/) 提供实现工具链。

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>
