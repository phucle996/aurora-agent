# Aurora Agent

Production-style Aurora runtime agent written in Go.

Agent này chạy trên host target để:

- Thu thập realtime metrics của node KVM
- Thu thập realtime metrics per-VM từ libvirt
- Stream dữ liệu về control plane qua `gRPC` hoặc `WebSocket`
- Heartbeat định kỳ về Admin qua gRPC mTLS để Admin seed agent connection info vào etcd
- Tự reconnect khi mất kết nối libvirt/backend
- Chạy nền bằng `systemd`

## Features

- Libvirt integration bằng `go-libvirt`
- Scheduler tách vòng poll VM và Node
- Stream abstraction (`grpc`)
- Admin heartbeat RPC (`ReportAgentHeartbeat`)
- TLS/mTLS support cho backend stream
- Structured logging (`json` hoặc `text`)

## Repository Structure

```text
cmd/agent/main.go                 # entrypoint
internal/config                   # env config + validation
internal/libvirt                  # libvirt connection + metrics readers
internal/collector                # scheduler + collectors
internal/stream                   # grpc/ws clients + encoder
internal/agent                    # lifecycle + health + shutdown
internal/system                   # host counters from /proc
proto/metrics/agent_metrics.proto # metrics payload contract
scripts/install.sh                # install binary + systemd
systemd/aurora-agent.service  # service unit
```

## Requirements

- Go `1.24+`
- Linux host có KVM/libvirt
- Quyền đọc libvirt socket (`qemu+unix:///system` mặc định)

## Quick Start (Local)

```bash
cd aurora-agent
go mod tidy
go test ./...
go run ./cmd/agent
```

## Install as Service

```bash
cd aurora-agent
chmod +x scripts/install.sh
./scripts/install.sh --admin-grpc-endpoint admin.aurora.local:443 --bootstrap-token --cluster-id default
```

Script sẽ:

- build binary `aurora-agent`
- install vào `/usr/local/bin/aurora-agent`
- copy unit file vào `/etc/systemd/system/aurora-agent.service`
- tạo file env `/etc/aurora-agent.env` (nếu chưa có)
- luôn dùng bootstrap token để xin cert mới nhất từ Admin
- luôn xóa cert/key cũ trước khi start để buộc rotate cert
- `systemctl enable --now aurora-agent.service`

## Runtime Flow

```text
connect libvirt
 -> poll VM metrics (1s)
 -> poll node metrics (3s)
 -> encode frame
 -> stream to backend
 -> health loop / reconnect
```

## Graceful Shutdown

- Nhận `SIGINT`/`SIGTERM` -> cancel toàn bộ collector loop
- Chờ goroutines dừng trong `AURORA_SHUTDOWN_TIMEOUT` (mặc định `20s`)
- Đóng stream sink và libvirt connection theo thứ tự
- `systemd` đã set `TimeoutStopSec=35` để đủ thời gian drain

## Main Environment Variables

Chi tiết đầy đủ xem `CONFIGURATION.md`.

- `AURORA_NODE_ID`
- `AURORA_LIBVIRT_URI`
- `AURORA_ADMIN_GRPC_ADDR`
- `AURORA_ADMIN_SERVER_NAME`
- `AURORA_ADMIN_SERVER_CA_PATH`
- `AURORA_ADMIN_CLIENT_CA_PATH`
- `AURORA_ADMIN_TLS_CERT_PATH`
- `AURORA_ADMIN_TLS_KEY_PATH`
- `AURORA_AGENT_BOOTSTRAP_TOKEN` (bắt buộc lúc install để rotate cert mới)
- `AURORA_AGENT_CLUSTER_ID` (`default` nếu không truyền)
- `AURORA_AGENT_IP`
- `AURORA_AGENT_HEARTBEAT_INTERVAL`

## Notes

- gRPC streaming hiện dùng JSON codec (không phụ thuộc generated pb trong runtime path).
- `proto/metrics/agent_metrics.proto` định nghĩa payload metrics chuẩn cho agent.

## License

Chưa khai báo license file. Bạn nên thêm `LICENSE` trước khi public repo.
