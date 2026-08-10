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
[更新日志](CHANGELOG.md) ·
[报告 Bug](https://github.com/JingYiJun/MoltSSH/issues/new?template=bug_report.yml) ·
[请求 Feature](https://github.com/JingYiJun/MoltSSH/issues/new?template=feature_request.yml)

![MoltSSH 架构：OpenSSH 使用 MoltSSH 作为 ProxyCommand，客户端在多条 WebSocket path 间恢复，服务端保持到 sshd 的稳定 TCP 连接。](docs/assets/moltssh-architecture.png)

## 目录

1. [项目介绍](#项目介绍)
2. [技术栈](#技术栈)
3. [开始使用](#开始使用)
4. [用法](#用法)
5. [故障排查](#故障排查)
6. [配置](#配置)
7. [路线图](#路线图)
8. [贡献](#贡献)
9. [安全](#安全)
10. [许可证](#许可证)
11. [联系](#联系)
12. [致谢](#致谢)

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
- 并行分阶段拨号、probe connection 复用、建议性的 last-known-good path
  首拨、session 内 heartbeat 和带 jitter 的重连退避。
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

使用 Go 安装最新 tag 版本：

```bash
go install github.com/jingyijun/moltssh/cmd/moltssh@latest
```

设置 `GOBIN` 时，二进制会安装到 `GOBIN`；否则会安装到
`$(go env GOPATH)/bin`。请确保对应目录已加入 `PATH`。

从源码构建：

```bash
git clone https://github.com/JingYiJun/MoltSSH.git
cd MoltSSH
go build -o moltssh ./cmd/moltssh
```

验证安装并查看构建来源：

```bash
moltssh version
moltssh --help
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

查看子命令帮助：

```bash
moltssh help proxy
moltssh help server
moltssh help probe
```

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>

## 故障排查

Proxy path 无法连接时，首先运行 probe：

```bash
moltssh probe --config ~/.config/moltssh/example.toml
```

对于 `moltssh probe`，`failed_phase` 会把故障定位到 `dns`、`tcp`、
`tls`、`websocket_upgrade` 或 `probe`。常见检查项：

- `dns`：检查 endpoint hostname 和 resolver。
- `tcp`：检查端口、relay 进程、防火墙和路由。
- `tls`：检查证书名称、信任链和 reverse proxy TLS 配置。
- `websocket_upgrade`：检查 `server.http_path`、reverse proxy 的
  WebSocket forwarding 和 `moltssh.v1` subprotocol。
- `probe`：WebSocket 已连接，但 MoltSSH ping/pong 检查失败；检查
  client/server 兼容性和 reverse proxy 的 frame forwarding。

Proxy session 日志可能改为报告 `failed_phase=moltssh_hello` 或
`unknown session`。此时应检查 client/server 兼容性，并确认 MoltSSH
server 进程没有重启。

运行 `moltssh help COMMAND` 可查看必需参数和子命令排障建议。能够给出
下一步诊断动作的错误会直接附带 hint。

> [!WARNING]
> MoltSSH 没有 application-layer authentication。不要把裸露的
> `moltssh server` WebSocket listener 暴露到公网。应绑定 loopback，或放在
> 受保护的 private access layer / 已认证 reverse proxy 后。详见
> [SECURITY.md](SECURITY.md)。

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

Path 性能与运行状态：

- 冷启动会并行 probe 所有 enabled paths，最多同时执行 8 个 probe。成功
  probe 的 WebSocket 会直接发送 `hello` 晋级为正式 session，不再重拨所选 path。
- Session 被 `accept` 后，`proxy` 只把已接受的 path name 写入建议性的
  last-known-good cache。设置 `XDG_CACHE_HOME` 时，路径为
  `$XDG_CACHE_HOME/moltssh/path-state/<config-hash>.json`；否则位于操作系统的
  user cache directory。hash 来自 config 文件 canonical path。
- 热启动会立即拨号已保存 path，同时在后台 probe 其他 paths。cache 缺失、
  过期、指向 disabled path、格式损坏或不可写时，都不会阻断 fallback。安全地
  删除该 cache 即可重置提示。
- Cache directory 使用 `0700`，文件使用 `0600`；JSON 只包含 `version` 和
  `path`。Session、epoch、offset、payload bytes、endpoint 和凭证仍然只存在于
  内存，不会写入 cache。
- Active session 在现有 WebSocket 内使用 application `ping`/`pong` heartbeat；
  只有 inactive paths 才新建 probe connection。Inactive probe 晋级时会复用
  同一个 WebSocket 发送 resume `hello`。
- 重连采用 capped exponential backoff + full jitter：base 为 `200ms`、cap 为
  `5s`，并把 delay 截断到剩余 `resume.timeout` 预算内。
- 每次正式尝试只输出一条 `event=proxy_dial`，包含 `dns`、`tcp`、`tls`、
  `websocket_upgrade`、`moltssh_hello`、`probe_rtt` 和 `total`。日志不包含
  endpoint 或 secret query values。这些优化不改变 `moltssh.v1` wire protocol，
  也不新增 TOML 字段。

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

欢迎通过聚焦的 issue 和 pull request 参与贡献。参与前请阅读
[CONTRIBUTING.md](CONTRIBUTING.md) 中的开发流程和
[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)。

<p align="right">(<a href="#readme-top">返回顶部</a>)</p>

## 安全

部署 MoltSSH 或报告漏洞前请阅读 [SECURITY.md](SECURITY.md)。涉及安全的报告
不应提交为公开 issue。

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
