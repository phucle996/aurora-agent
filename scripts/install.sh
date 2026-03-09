#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_NAME="install.sh"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

APP_NAME="${AURORA_AGENT_BIN_NAME:-aurora-agent}"
INSTALL_DIR="${AURORA_AGENT_INSTALL_DIR:-/usr/local/bin}"
BIN_PATH="${INSTALL_DIR}/${APP_NAME}"
SERVICE_NAME="${AURORA_AGENT_SERVICE_NAME:-aurora-agent.service}"
SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}"
ENV_FILE="${AURORA_AGENT_ENV_FILE:-/etc/aurora-agent.env}"
VERSION="${AURORA_AGENT_VERSION:-latest}"
TMP_DIR=""
CLI_ADMIN_GRPC_ENDPOINT=""
CLI_ADMIN_SERVER_NAME=""
CLI_ADMIN_CLIENT_CN=""
CLI_ADMIN_TLS_CA_PATH=""
CLI_ADMIN_TLS_CERT_PATH=""
CLI_ADMIN_TLS_KEY_PATH=""
CLI_HEARTBEAT_INTERVAL=""
CLI_BOOTSTRAP_TOKEN=""
CLI_CLUSTER_ID=""
CLI_AGENT_IP=""
CLI_AGENT_GRPC_ENDPOINT=""

log() {
  printf '[%s] %s\n' "$SCRIPT_NAME" "$1"
}

warn() {
  printf '[%s][warn] %s\n' "$SCRIPT_NAME" "$1" >&2
}

trap 'rc=$?; line="${BASH_LINENO[0]:-$LINENO}"; cmd="${BASH_COMMAND:-unknown}"; printf "[%s][error] rc=%s line=%s cmd=%s\n" "$SCRIPT_NAME" "$rc" "$line" "$cmd" >&2' ERR

run_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
    return
  fi
  if command -v sudo >/dev/null 2>&1; then
    if [ -n "${SUDO_PASSWORD_B64:-}" ]; then
      local sudo_password
      sudo_password="$(printf '%s' "$SUDO_PASSWORD_B64" | base64 -d 2>/dev/null || true)"
      if [ -n "$sudo_password" ]; then
        printf '%s\n' "$sudo_password" | sudo -S -p '' "$@"
        return
      fi
    fi
    sudo "$@"
    return
  fi
  echo "This installer requires root or sudo." >&2
  exit 1
}

cleanup_tmp_dir() {
  if [ -n "${TMP_DIR:-}" ] && [ -d "${TMP_DIR:-}" ]; then
    rm -rf "${TMP_DIR}" || true
  fi
}

require_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "Missing required command: ${cmd}" >&2
    exit 1
  fi
}

parse_args() {
  while [ "$#" -gt 0 ]; do
    case "$1" in
      --admin-grpc-endpoint)
        if [ "$#" -lt 2 ]; then
          echo "missing value for --admin-grpc-endpoint" >&2
          exit 1
        fi
        CLI_ADMIN_GRPC_ENDPOINT="$(printf '%s' "$2" | xargs)"
        shift 2
        ;;
      --admin-server-name)
        if [ "$#" -lt 2 ]; then
          echo "missing value for --admin-server-name" >&2
          exit 1
        fi
        CLI_ADMIN_SERVER_NAME="$(printf '%s' "$2" | xargs)"
        shift 2
        ;;
      --admin-client-cn)
        if [ "$#" -lt 2 ]; then
          echo "missing value for --admin-client-cn" >&2
          exit 1
        fi
        CLI_ADMIN_CLIENT_CN="$(printf '%s' "$2" | xargs)"
        shift 2
        ;;
      --admin-tls-ca-path)
        if [ "$#" -lt 2 ]; then
          echo "missing value for --admin-tls-ca-path" >&2
          exit 1
        fi
        CLI_ADMIN_TLS_CA_PATH="$(printf '%s' "$2" | xargs)"
        shift 2
        ;;
      --admin-tls-cert-path)
        if [ "$#" -lt 2 ]; then
          echo "missing value for --admin-tls-cert-path" >&2
          exit 1
        fi
        CLI_ADMIN_TLS_CERT_PATH="$(printf '%s' "$2" | xargs)"
        shift 2
        ;;
      --admin-tls-key-path)
        if [ "$#" -lt 2 ]; then
          echo "missing value for --admin-tls-key-path" >&2
          exit 1
        fi
        CLI_ADMIN_TLS_KEY_PATH="$(printf '%s' "$2" | xargs)"
        shift 2
        ;;
      --heartbeat-interval)
        if [ "$#" -lt 2 ]; then
          echo "missing value for --heartbeat-interval" >&2
          exit 1
        fi
        CLI_HEARTBEAT_INTERVAL="$(printf '%s' "$2" | xargs)"
        shift 2
        ;;
      --bootstrap-token)
        if [ "$#" -lt 2 ]; then
          echo "missing value for --bootstrap-token" >&2
          exit 1
        fi
        CLI_BOOTSTRAP_TOKEN="$(printf '%s' "$2" | xargs)"
        shift 2
        ;;
      --cluster-id)
        if [ "$#" -lt 2 ]; then
          echo "missing value for --cluster-id" >&2
          exit 1
        fi
        CLI_CLUSTER_ID="$(printf '%s' "$2" | xargs)"
        shift 2
        ;;
      --agent-ip)
        if [ "$#" -lt 2 ]; then
          echo "missing value for --agent-ip" >&2
          exit 1
        fi
        CLI_AGENT_IP="$(printf '%s' "$2" | xargs)"
        shift 2
        ;;
      --agent-grpc-endpoint)
        if [ "$#" -lt 2 ]; then
          echo "missing value for --agent-grpc-endpoint" >&2
          exit 1
        fi
        CLI_AGENT_GRPC_ENDPOINT="$(printf '%s' "$2" | xargs)"
        shift 2
        ;;
      --help|-h)
        cat <<'EOF'
Usage: install.sh [--admin-grpc-endpoint host:port|https://host[:port]] [--admin-server-name host] [--admin-client-cn cn] [--admin-tls-ca-path path] [--admin-tls-cert-path path] [--admin-tls-key-path path] [--heartbeat-interval 15s] [--bootstrap-token token] [--cluster-id id] [--agent-ip ip] [--agent-grpc-endpoint host:port]
EOF
        exit 0
        ;;
      *)
        echo "unknown argument: $1" >&2
        exit 1
        ;;
    esac
  done
}

set_env_kv() {
  local file="$1"
  local key="$2"
  local value="$3"

  local escaped="$value"
  escaped="${escaped//\\/\\\\}"
  escaped="${escaped//&/\\&}"
  escaped="${escaped//|/\\|}"

  if run_root grep -Eq "^${key}=" "$file"; then
    run_root sed -i "s|^${key}=.*|${key}=${escaped}|g" "$file"
  else
    run_root bash -lc "printf '%s=%s\n' '${key}' '${value}' >> '${file}'"
  fi
}

generate_node_id() {
  if command -v uuidgen >/dev/null 2>&1; then
    uuidgen | tr '[:upper:]' '[:lower:]'
    return
  fi
  if [ -r /proc/sys/kernel/random/uuid ]; then
    cat /proc/sys/kernel/random/uuid | tr '[:upper:]' '[:lower:]'
    return
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 16 | sed 's/^\(........\)\(....\)\(....\)\(....\)\(............\)$/\1-\2-\3-\4-\5/'
    return
  fi
  local raw
  raw="$(date +%s%N)$RANDOM$RANDOM"
  raw="${raw}00000000000000000000000000000000"
  raw="${raw:0:32}"
  printf '%s' "$raw" | sed 's/^\(........\)\(....\)\(....\)\(....\)\(............\)$/\1-\2-\3-\4-\5/'
}

read_env_kv() {
  local file="$1"
  local key="$2"
  if ! run_root test -f "$file"; then
    printf ''
    return
  fi
  run_root bash -lc "grep -E '^${key}=' '${file}' | tail -n1 | cut -d= -f2-" | tr -d '\r' | xargs || true
}

ensure_node_id() {
  local node_id
  node_id="$(printf '%s' "${AURORA_NODE_ID:-}" | xargs || true)"
  if [ -z "$node_id" ]; then
    node_id="$(read_env_kv "$ENV_FILE" "AURORA_NODE_ID")"
  fi
  if [ -z "$node_id" ]; then
    node_id="$(generate_node_id)"
    log "generated node_id=${node_id}"
  fi
  set_env_kv "$ENV_FILE" "AURORA_NODE_ID" "$node_id"
  printf '%s' "$node_id"
}

apply_runtime_config() {
  local admin_grpc_endpoint
  admin_grpc_endpoint="$(printf '%s' "${CLI_ADMIN_GRPC_ENDPOINT:-${AURORA_ADMIN_GRPC_ENDPOINT:-}}" | xargs || true)"
  if [ -z "$admin_grpc_endpoint" ]; then
    echo "missing admin grpc endpoint: provide --admin-grpc-endpoint or AURORA_ADMIN_GRPC_ENDPOINT" >&2
    exit 1
  fi
  set_env_kv "$ENV_FILE" "AURORA_ADMIN_GRPC_ADDR" "$admin_grpc_endpoint"

  local admin_server_name
  admin_server_name="$(printf '%s' "${CLI_ADMIN_SERVER_NAME:-${AURORA_ADMIN_SERVER_NAME:-}}" | xargs || true)"
  if [ -n "$admin_server_name" ]; then
    set_env_kv "$ENV_FILE" "AURORA_ADMIN_SERVER_NAME" "$admin_server_name"
  fi
  local admin_client_cn
  admin_client_cn="$(printf '%s' "${CLI_ADMIN_CLIENT_CN:-${AURORA_AGENT_ADMIN_CLIENT_CN:-$admin_server_name}}" | xargs || true)"
  if [ -n "$admin_client_cn" ]; then
    set_env_kv "$ENV_FILE" "AURORA_AGENT_ADMIN_CLIENT_CN" "$admin_client_cn"
  fi

  local admin_tls_ca
  admin_tls_ca="$(printf '%s' "${CLI_ADMIN_TLS_CA_PATH:-${AURORA_ADMIN_TLS_CA_PATH:-/etc/aurora/certs/ca.crt}}" | xargs || true)"
  local admin_tls_cert
  admin_tls_cert="$(printf '%s' "${CLI_ADMIN_TLS_CERT_PATH:-${AURORA_ADMIN_TLS_CERT_PATH:-/etc/aurora/certs/agent.crt}}" | xargs || true)"
  local admin_tls_key
  admin_tls_key="$(printf '%s' "${CLI_ADMIN_TLS_KEY_PATH:-${AURORA_ADMIN_TLS_KEY_PATH:-/etc/aurora/certs/agent.key}}" | xargs || true)"

  if [ -z "$admin_tls_ca" ] || [ -z "$admin_tls_cert" ] || [ -z "$admin_tls_key" ]; then
    echo "admin tls paths are required (ca/cert/key)" >&2
    exit 1
  fi
  set_env_kv "$ENV_FILE" "AURORA_ADMIN_TLS_CA_PATH" "$admin_tls_ca"
  set_env_kv "$ENV_FILE" "AURORA_ADMIN_TLS_CERT_PATH" "$admin_tls_cert"
  set_env_kv "$ENV_FILE" "AURORA_ADMIN_TLS_KEY_PATH" "$admin_tls_key"

  local heartbeat_interval
  heartbeat_interval="$(printf '%s' "${CLI_HEARTBEAT_INTERVAL:-${AURORA_AGENT_HEARTBEAT_INTERVAL:-15s}}" | xargs || true)"
  set_env_kv "$ENV_FILE" "AURORA_AGENT_HEARTBEAT_INTERVAL" "$heartbeat_interval"

  local bootstrap_token
  bootstrap_token="$(printf '%s' "${CLI_BOOTSTRAP_TOKEN:-${AURORA_AGENT_BOOTSTRAP_TOKEN:-}}" | xargs || true)"
  if [ -n "$bootstrap_token" ]; then
    set_env_kv "$ENV_FILE" "AURORA_AGENT_BOOTSTRAP_TOKEN" "$bootstrap_token"
  fi

  local cluster_id
  cluster_id="$(printf '%s' "${CLI_CLUSTER_ID:-${AURORA_AGENT_CLUSTER_ID:-}}" | xargs || true)"
  if [ -n "$cluster_id" ]; then
    set_env_kv "$ENV_FILE" "AURORA_AGENT_CLUSTER_ID" "$cluster_id"
  fi

  local agent_ip
  agent_ip="$(printf '%s' "${CLI_AGENT_IP:-${AURORA_AGENT_IP:-}}" | xargs || true)"
  if [ -n "$agent_ip" ]; then
    set_env_kv "$ENV_FILE" "AURORA_AGENT_IP" "$agent_ip"
  fi

  local agent_grpc_endpoint
  agent_grpc_endpoint="$(printf '%s' "${CLI_AGENT_GRPC_ENDPOINT:-${AURORA_AGENT_GRPC_ENDPOINT:-}}" | xargs || true)"
  if [ -n "$agent_grpc_endpoint" ]; then
    set_env_kv "$ENV_FILE" "AURORA_AGENT_GRPC_ENDPOINT" "$agent_grpc_endpoint"
  fi

  log "runtime config admin_grpc_addr=${admin_grpc_endpoint} heartbeat=${heartbeat_interval} admin_client_cn=${admin_client_cn:-<unset>}"
}

resolve_repo_default() {
  local env_repo="${AURORA_AGENT_GITHUB_REPO:-}"
  if [ -n "$env_repo" ]; then
    printf '%s' "$env_repo"
    return
  fi

  if command -v git >/dev/null 2>&1 && [ -d "${ROOT_DIR}/.git" ]; then
    local remote
    remote="$(git -C "$ROOT_DIR" config --get remote.origin.url 2>/dev/null || true)"
    if [ -n "$remote" ]; then
      remote="${remote#git@github.com:}"
      remote="${remote#https://github.com/}"
      remote="${remote%.git}"
      if [ -n "$remote" ] && printf '%s' "$remote" | grep -q '/'; then
        printf '%s' "$remote"
        return
      fi
    fi
  fi

  # Fallback default; override via AURORA_AGENT_GITHUB_REPO if needed.
  printf 'phucle996/aurora-agent'
}

resolve_arch() {
  case "$(uname -m 2>/dev/null || true)" in
    x86_64|amd64) printf 'amd64' ;;
    aarch64|arm64) printf 'arm64' ;;
    *)
      echo "Unsupported architecture: $(uname -m 2>/dev/null || echo unknown)" >&2
      exit 1
      ;;
  esac
}

download_file() {
  local url="$1"
  local dst="$2"
  local token="${AURORA_AGENT_GITHUB_TOKEN:-}"

  if command -v curl >/dev/null 2>&1; then
    if [ -n "$token" ]; then
      curl -fL --retry 3 --retry-delay 2 --connect-timeout 10 \
        -H "Authorization: Bearer ${token}" \
        -o "$dst" "$url"
    else
      curl -fL --retry 3 --retry-delay 2 --connect-timeout 10 \
        -o "$dst" "$url"
    fi
    return
  fi

  if command -v wget >/dev/null 2>&1; then
    if [ -n "$token" ]; then
      wget --tries=3 --timeout=10 \
        --header="Authorization: Bearer ${token}" \
        -O "$dst" "$url"
    else
      wget --tries=3 --timeout=10 -O "$dst" "$url"
    fi
    return
  fi

  echo "curl/wget not available for download" >&2
  exit 1
}

verify_checksum() {
  local file="$1"
  local expected="$2"

  if command -v sha256sum >/dev/null 2>&1; then
    local actual
    actual="$(sha256sum "$file" | awk '{print $1}')"
    [ "$actual" = "$expected" ]
    return
  fi

  if command -v shasum >/dev/null 2>&1; then
    local actual
    actual="$(shasum -a 256 "$file" | awk '{print $1}')"
    [ "$actual" = "$expected" ]
    return
  fi

  warn "sha256 tool not found (sha256sum/shasum); skipping checksum verification"
  return 0
}

install_systemd_unit() {
  local local_unit="${ROOT_DIR}/systemd/aurora-agent.service"
  if [ -f "$local_unit" ]; then
    run_root install -m 0644 "$local_unit" "$SERVICE_PATH"
    return
  fi

  run_root bash -lc "cat > '${SERVICE_PATH}' <<'EOF'
[Unit]
Description=Aurora Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
EnvironmentFile=-/etc/aurora-agent.env
ExecStart=/usr/local/bin/aurora-agent
Restart=always
RestartSec=3
KillSignal=SIGTERM
TimeoutStopSec=35
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF"
}

ensure_env_file() {
  if run_root test -f "$ENV_FILE"; then
    return
  fi

  run_root bash -lc "cat > '${ENV_FILE}' <<'EOF'
AURORA_NODE_ID=
AURORA_AGENT_VERSION=
AURORA_AGENT_PLATFORM=linux
AURORA_LIBVIRT_URI=qemu+unix:///system
AURORA_AGENT_PROBE_ADDR=0.0.0.0:7443
AURORA_AGENT_GRPC_ENDPOINT=
AURORA_AGENT_CLUSTER_ID=
AURORA_AGENT_IP=
AURORA_AGENT_BOOTSTRAP_TOKEN=
AURORA_ADMIN_GRPC_ADDR=
AURORA_ADMIN_SERVER_NAME=
AURORA_ADMIN_TLS_CA_PATH=/etc/aurora/certs/ca.crt
AURORA_ADMIN_TLS_CERT_PATH=/etc/aurora/certs/agent.crt
AURORA_ADMIN_TLS_KEY_PATH=/etc/aurora/certs/agent.key
AURORA_AGENT_HEARTBEAT_INTERVAL=15s
AURORA_LOG_JSON=true
AURORA_LOG_LEVEL=info
AURORA_NODE_POLL_INTERVAL=3s
AURORA_VM_POLL_INTERVAL=1s
AURORA_SHUTDOWN_TIMEOUT=20s
EOF"
}

ensure_agent_version() {
  local agent_version
  agent_version="$(printf '%s' "${AURORA_AGENT_VERSION:-${VERSION:-latest}}" | xargs || true)"
  if [ -z "$agent_version" ]; then
    agent_version="latest"
  fi
  set_env_kv "$ENV_FILE" "AURORA_AGENT_VERSION" "$agent_version"
  printf '%s' "$agent_version"
}

main() {
  parse_args "$@"
  require_cmd tar
  # Validate sudo/root availability early so installer fails fast with clear error.
  run_root true
  local repo
  repo="$(resolve_repo_default)"
  local arch
  arch="$(resolve_arch)"

  local asset="${APP_NAME}_linux_${arch}.tar.gz"
  local checksum_asset="checksums.txt"

  local base_url
  if [ "$VERSION" = "latest" ]; then
    base_url="https://github.com/${repo}/releases/latest/download"
  else
    base_url="https://github.com/${repo}/releases/download/${VERSION}"
  fi

  TMP_DIR="$(mktemp -d /tmp/${APP_NAME}-install.XXXXXX)"
  trap cleanup_tmp_dir EXIT

  local tarball="${TMP_DIR}/${asset}"
  local checksums="${TMP_DIR}/${checksum_asset}"

  log "downloading release asset repo=${repo} version=${VERSION} arch=${arch}"
  download_file "${base_url}/${asset}" "$tarball"
  download_file "${base_url}/${checksum_asset}" "$checksums"

  local expected
  expected="$(awk -v asset="$asset" '{name=$2; sub(/^.*\//, "", name); if(name==asset){print $1; exit}}' "$checksums")"
  if [ -z "$expected" ]; then
    echo "Cannot find checksum for ${asset} in ${checksum_asset}" >&2
    exit 1
  fi

  if ! verify_checksum "$tarball" "$expected"; then
    echo "Checksum verification failed for ${asset}" >&2
    exit 1
  fi
  log "checksum verification passed"

  tar -xzf "$tarball" -C "$TMP_DIR"
  local extracted="${TMP_DIR}/${APP_NAME}_linux_${arch}"
  if [ ! -f "$extracted" ]; then
    echo "Extracted binary not found: ${extracted}" >&2
    exit 1
  fi

  run_root mkdir -p "$INSTALL_DIR"
  run_root install -m 0755 "$extracted" "$BIN_PATH"
  log "installed binary -> ${BIN_PATH}"

  ensure_env_file
  local node_id
  node_id="$(ensure_node_id)"
  log "runtime node_id=${node_id}"
  local agent_version
  agent_version="$(ensure_agent_version)"
  log "runtime agent_version=${agent_version}"
  apply_runtime_config
  install_systemd_unit

  if command -v systemctl >/dev/null 2>&1; then
    run_root systemctl daemon-reload
    run_root systemctl enable --now "$SERVICE_NAME"
    run_root systemctl restart "$SERVICE_NAME"
    if run_root systemctl is-active --quiet "$SERVICE_NAME"; then
      log "service is active: ${SERVICE_NAME}"
    else
      warn "service is not active: ${SERVICE_NAME}"
      run_root systemctl status "$SERVICE_NAME" --no-pager || true
      exit 1
    fi
  else
    warn "systemctl not found; service was not started"
  fi

  log "installation completed"
}

main "$@"
