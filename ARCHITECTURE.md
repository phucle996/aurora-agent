# Architecture

## High-Level

```text
+--------------------+        gRPC (mTLS)             +----------------------+
|   Aurora Agent     |  ---------------------------->  |   Aurora Admin       |
+---------+----------+                                 +----------+-----------+
          |
          | libvirt RPC
          v
+--------------------+
| libvirt / KVM host |
+--------------------+
```

## Internal Pipeline

```text
ConnManager (libvirt)
  -> NodeMetricsReader / VMMetricsReader
  -> Collector Scheduler
  -> Heartbeat Reporter (gRPC mTLS)
  -> Admin Runtime Service
```

## Core Components

### 1) Connection Manager

`internal/libvirt/conn.go`

- Quản lý `go-libvirt` client singleton
- Có `Connect`, `Client`, `Reconnect`, `Healthy`, `Close`
- Reconnect loop có jitter

### 2) Node Metrics Reader

`internal/libvirt/node_metrics.go`

- Dùng libvirt API:
  - `NodeGetInfo`
  - `NodeGetCPUStats`
  - `NodeGetMemoryStats`
- Kết hợp `/proc` counters cho disk/net/load

### 3) VM Metrics Reader

`internal/libvirt/vm_metrics.go`

- Dùng libvirt API:
  - `ConnectListAllDomains`
  - `ConnectGetAllDomainStats`
- Parse CPU/RAM/Block/Net từ typed params

### 4) Scheduler

`internal/collector/scheduler.go`

- Loop song song:
  - VM loop: mặc định mỗi `1s`
  - Node loop: mặc định mỗi `3s`
- Khi lỗi collector/send: backoff rồi retry

### 5) Admin Heartbeat Layer

`internal/adminrpc/`

- `heartbeat_client.go`: gọi `RuntimeService/ReportAgentHeartbeat`
- Payload gửi identity, version, probe/grpc endpoint
- Admin ack thành công => seed/update agent connection info vào etcd

### 6) Agent Lifecycle

`internal/agent/`

- Start: connect libvirt + khởi chạy scheduler
- Health loop: kiểm tra libvirt, tự reconnect
- Event loop: heartbeat event monitor
- Shutdown: đóng stream + disconnect libvirt

## Reliability Strategy

- Libvirt health-check định kỳ
- Reconnect khi stream/libvirt lỗi
- Context-aware cancellation cho graceful shutdown
- Structured logs để truy vết production

## Security

- Hỗ trợ mTLS bắt buộc khi agent gọi Admin
- Libvirt endpoint có thể local unix hoặc remote URI

## Installer Direction

- Kiến trúc installer phase 0 được chốt tại [AURORA_INSTALLER_PHASE0.md](/home/phucle/Desktop/project/AURORA_INSTALLER_PHASE0.md).
- Roadmap production-grade được chốt tại [AURORA_INSTALLER_PRODUCTION_GRADE.md](/home/phucle/Desktop/project/AURORA_INSTALLER_PRODUCTION_GRADE.md).
- `aurora-agent` sẽ trở thành execution plane cho module install:
  - download artifact bundle
  - verify checksum/signature
  - render env/systemd/nginx
  - install/restart/healthcheck

## Service Install Execution Flow

Hiện tại `aurora-agent` chỉ support installer runtime `linux-systemd`.

```mermaid
sequenceDiagram
    autonumber
    participant Admin as Aurora Admin
    participant RPC as Agent RPC Service
    participant Engine as Installer Engine
    participant FS as Filesystem
    participant Systemd as systemd/nginx
    participant State as local state

    Admin->>RPC: InstallModuleStream / InstallModule
    RPC->>Engine: executeInstallModule()
    Engine->>Engine: validate request
    Engine->>FS: download artifact bundle
    Engine->>FS: verify checksum
    Engine->>FS: unpack tar.gz
    Engine->>FS: load manifest.json
    Engine->>FS: write env + inline files
    Engine->>FS: install binary
    Engine->>FS: render systemd unit + nginx site
    Engine->>Systemd: systemctl daemon-reload
    Engine->>Systemd: enable + restart service
    Engine->>Systemd: nginx -t + reload nginx
    Engine->>Engine: healthcheck
    Engine->>State: persist installed_modules.json
    Engine->>State: append audit event
    RPC-->>Admin: stream logs + result
```

```mermaid
flowchart TD
    A[Install request] --> B[Validate]
    B --> C[Download bundle]
    C --> D[Verify checksum]
    D --> E[Unpack artifact]
    E --> F[Load manifest]
    F --> G[Write env and files]
    G --> H[Install binary]
    H --> I[Render service and nginx templates]
    I --> J[systemctl daemon-reload]
    J --> K[enable and restart service]
    K --> L[nginx test and reload]
    L --> M[Healthcheck]
    M --> N[Persist installed_modules.json]
    N --> O[Append audit event]
    O --> P[Return result to Admin]
```

### Status Vocabulary

Agent inventory dùng các trạng thái sau để khớp với Admin reconcile:

- `installing`
- `installed`
- `failed`
- `unknown`
