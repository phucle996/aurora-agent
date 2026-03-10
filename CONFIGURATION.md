# Configuration

Aurora Agent đọc config từ environment variables.

## Required

| Variable | Description |
|---|---|
| `AURORA_NODE_ID` | Agent identity |
| `AURORA_ADMIN_GRPC_ADDR` | Admin gRPC endpoint (`host:port` hoặc `https://host[:port]`) |
| `AURORA_ADMIN_TLS_CA_PATH` | CA PEM path để verify Admin |
| `AURORA_ADMIN_TLS_CERT_PATH` | Client cert PEM path (đường dẫn sẽ được agent ghi cert mới sau bootstrap) |
| `AURORA_ADMIN_TLS_KEY_PATH` | Client key PEM path (đường dẫn sẽ được agent ghi key mới sau bootstrap) |
| `AURORA_AGENT_BOOTSTRAP_TOKEN` | Bootstrap token (required khi install để xin cert mới và ghi đè cert cũ) |

## Heartbeat / Runtime

| Variable | Default | Description |
|---|---|---|
| `AURORA_AGENT_HEARTBEAT_INTERVAL` | `15s` | Chu kỳ heartbeat lên Admin |
| `AURORA_AGENT_PROBE_ADDR` | `0.0.0.0:7443` | Agent probe listen addr |
| `AURORA_AGENT_GRPC_ENDPOINT` | empty | Endpoint Admin dùng để gọi ngược agent (optional) |
| `AURORA_AGENT_CLUSTER_ID` | empty | Cluster membership id khi bootstrap |
| `AURORA_AGENT_IP` | empty | IP address advertise khi bootstrap |
| `AURORA_AGENT_PLATFORM` | `linux` | Platform label để seed etcd |
| `AURORA_LIBVIRT_URI` | `qemu+unix:///system` | Libvirt URI |
| `AURORA_HEALTH_INTERVAL` | `10s` | Libvirt health tick |
| `AURORA_RECONNECT_INTERVAL` | `4s` | Libvirt reconnect base interval |
| `AURORA_SHUTDOWN_TIMEOUT` | `20s` | Graceful shutdown timeout |

## Optional

| Variable | Default |
|---|---|
| `AURORA_ADMIN_SERVER_NAME` | empty (auto infer từ endpoint host) |
| `AURORA_AGENT_ADMIN_CLIENT_CN` | `AURORA_ADMIN_SERVER_NAME` hoặc `admin.aurora.local` |
| `AURORA_LOG_LEVEL` | `info` (`debug`, `warn`, `error`) |

## Example

```env
AURORA_NODE_ID=node-a1
AURORA_AGENT_PLATFORM=linux
AURORA_LIBVIRT_URI=qemu+unix:///system
AURORA_AGENT_PROBE_ADDR=0.0.0.0:7443
AURORA_AGENT_GRPC_ENDPOINT=node-a1.aurora.local:7443

AURORA_ADMIN_GRPC_ADDR=admin.aurora.local:443
AURORA_ADMIN_SERVER_NAME=admin.aurora.local
AURORA_ADMIN_TLS_CA_PATH=/etc/aurora/certs/ca.crt
AURORA_ADMIN_TLS_CERT_PATH=/etc/aurora/certs/agent.crt
AURORA_ADMIN_TLS_KEY_PATH=/etc/aurora/certs/agent.key
AURORA_AGENT_HEARTBEAT_INTERVAL=15s

AURORA_LOG_LEVEL=info
```
