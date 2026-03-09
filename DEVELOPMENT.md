# Development Guide

## Run locally

```bash
cd vm-agent
go mod tidy
go test ./...
go run ./cmd/agent
```

## Build binary

```bash
go build -o aurora-agent ./cmd/agent
```

## Code style

- Go idiomatic, context-first
- Structured log qua `log/slog`
- Không panic trong runtime path
- Reconnect logic phải idempotent

## Extend metrics

### Add host metric

1. Add reader function trong `internal/system/`
2. Map vào `internal/libvirt/node_metrics.go`
3. Thêm field vào `internal/model/node.go`
4. Cập nhật `proto/metrics/agent_metrics.proto` nếu backend cần strict schema

### Add VM metric

1. Parse thêm typed params ở `internal/libvirt/vm_metrics.go`
2. Add field trong `internal/model/vm.go`
3. Cập nhật proto nếu cần

## Testing focus

- Unit test parser/mapper logic
- Test reconnect behavior (libvirt down/up)
- Test stream fallback (network flapping)

## Common troubleshooting

- Permission denied libvirt socket:
  - Chạy agent bằng root hoặc user có quyền libvirt group
- No domains returned:
  - Kiểm tra host có VM active/inactive trong libvirt
- Stream fail:
  - Kiểm tra endpoint/token/TLS cert chain
