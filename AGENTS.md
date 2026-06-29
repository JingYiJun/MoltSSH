Always reply in 中文.

## Project

MoltSSH 是一个 Go CLI 项目，目标是实现基于 `quic-go` 的 OpenSSH `ProxyCommand` 可迁移连接工具。

## Engineering Rules

- 保持最小实现；不要提前抽象 provider/plugin。
- CLI 二进制名固定为 `moltssh`。
- 第一阶段只实现 UDP/QUIC 主路径；TCP/WebSocket resume 是 fallback，不混入 MVP。
- 新增非平凡逻辑时，留下最小可运行测试。
- 优先使用 Go 标准库；只有接入 QUIC 时再引入 `github.com/quic-go/quic-go`。
- 不在日志、测试快照或示例配置中写入真实私钥、token、证书或主机凭证。

## Commands

```bash
go test ./...
GOCACHE=/tmp/moltssh-go-build go test ./...
```
