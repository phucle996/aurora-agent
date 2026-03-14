# Configuration

Aurora Agent đọc config từ environment variables.

## Required

| Variable | Description |
|---|---|
| `AURORA_NODE_ID` | Agent identity |
| `AURORA_ADMIN_GRPC_ADDR` | Admin gRPC endpoint (`host:port` hoặc `https://host[:port]`) |
| `AURORA_ADMIN_SERVER_CA_PATH` | CA PEM path để verify **Admin server cert** (thường là `/etc/aurora/certs/ca.crt`) |
| `AURORA_ADMIN_CLIENT_CA_PATH` | CA PEM path để verify **Admin client cert** khi Admin gọi ngược agent probe (thường là `/etc/aurora/certs/agent-ca.crt`) |
| `AURORA_ADMIN_TLS_CLIENT_CERT_PATH` | Client cert PEM path cho `agent -> admin` (thường là `/etc/aurora/certs/agent-client.crt`) |
| `AURORA_ADMIN_TLS_CLIENT_KEY_PATH` | Client key PEM path cho `agent -> admin` (thường là `/etc/aurora/certs/agent-client.key`) |
| `AURORA_AGENT_TLS_SERVER_CERT_PATH` | Serving cert PEM path cho `admin -> agent` (thường là `/etc/aurora/certs/agent-server.crt`) |
| `AURORA_AGENT_TLS_SERVER_KEY_PATH` | Serving key PEM path cho `admin -> agent` (thường là `/etc/aurora/certs/agent-server.key`) |
| `AURORA_AGENT_BOOTSTRAP_TOKEN` | Bootstrap token (required khi install để xin cert mới và ghi đè cert cũ) |

## Heartbeat / Runtime

| Variable | Default | Description |
|---|---|---|
| `AURORA_AGENT_HEARTBEAT_INTERVAL` | `15s` | Chu kỳ heartbeat lên Admin |
| `AURORA_AGENT_PROBE_ADDR` | `0.0.0.0:7443` | Agent probe listen addr |
| `AURORA_AGENT_GRPC_ENDPOINT` | empty | Endpoint Admin dùng để gọi ngược agent (optional) |
| `AURORA_AGENT_CLUSTER_ID` | `default` | Cluster membership id khi bootstrap |
| `AURORA_AGENT_IP` | empty | IP address advertise khi bootstrap |
| `AURORA_AGENT_PLATFORM` | `linux` | Platform label để seed etcd |
| `AURORA_INSTALL_ALLOWED_MODULES` | `ums,platform,paas,dbaas` | Danh sách module agent cho phép bundle install |
| `AURORA_INSTALL_ALLOWED_ARTIFACT_HOSTS` | `github.com,release-assets.githubusercontent.com,objects.githubusercontent.com` | Danh sách host được phép tải artifact |
| `AURORA_INSTALL_AUDIT_LOG_PATH` | `/var/lib/aurora-agent/install_audit.jsonl` | Audit log JSONL cho install/restart/uninstall |
| `AURORA_LIBVIRT_URI` | `qemu+unix:///system` | Libvirt URI |
| `AURORA_HEALTH_INTERVAL` | `10s` | Libvirt health tick |
| `AURORA_RECONNECT_INTERVAL` | `4s` | Libvirt reconnect base interval |
| `AURORA_SHUTDOWN_TIMEOUT` | `20s` | Graceful shutdown timeout |

## Optional

| Variable | Default |
|---|---|
| `AURORA_ADMIN_SERVER_NAME` | empty (auto infer từ endpoint host) |
| `AURORA_AGENT_ADMIN_CLIENT_CN` | `AURORA_ADMIN_SERVER_NAME` hoặc `admin.aurora.local` |
| `AURORA_ADMIN_TLS_CA_PATH` | legacy alias, fallback cho `AURORA_ADMIN_SERVER_CA_PATH` |
| `AURORA_LOG_LEVEL` | `info` (`debug`, `warn`, `error`) |

## Example

```env
AURORA_NODE_ID=node-a1
AURORA_AGENT_PLATFORM=linux
AURORA_LIBVIRT_URI=qemu+unix:///system
AURORA_AGENT_PROBE_ADDR=0.0.0.0:7443
AURORA_AGENT_GRPC_ENDPOINT=node-a1.aurora.local:7443

AURORA_ADMIN_GRPC_ADDR=admin.aurora.local:50544
AURORA_ADMIN_SERVER_NAME=admin.aurora.local
AURORA_ADMIN_SERVER_CA_PATH=/etc/aurora/certs/ca.crt
AURORA_ADMIN_CLIENT_CA_PATH=/etc/aurora/certs/agent-ca.crt
AURORA_ADMIN_TLS_CLIENT_CERT_PATH=/etc/aurora/certs/agent-client.crt
AURORA_ADMIN_TLS_CLIENT_KEY_PATH=/etc/aurora/certs/agent-client.key
AURORA_AGENT_TLS_SERVER_CERT_PATH=/etc/aurora/certs/agent-server.crt
AURORA_AGENT_TLS_SERVER_KEY_PATH=/etc/aurora/certs/agent-server.key
AURORA_AGENT_HEARTBEAT_INTERVAL=15s

AURORA_LOG_LEVEL=info
```

## Install Security Notes

- Agent chỉ cho bundle install các module nằm trong `AURORA_INSTALL_ALLOWED_MODULES`.
- `artifact_url` phải đi qua host nằm trong `AURORA_INSTALL_ALLOWED_ARTIFACT_HOSTS`.
- Với bundle-managed module, agent còn kiểm tra:
  - đúng `github.com/<owner>/<repo>/releases/download/...`
  - đúng `repo slug` theo module
  - đúng `bundle asset base` theo module
- Install log/error sẽ tự redact giá trị secret như:
  - PEM blocks
  - password/token/secret-like env values
  - inline TLS file contents
