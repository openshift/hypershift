#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../../.." && pwd)"
runtime="${RUNTIME:-$(bash "${repo_root}/hack/utils.sh" get_container_engine)}"
image_tag="${IMAGE_TAG:-localhost/hypershift-shared-ingress:smoke}"
container_name="shared-ingress-smoke-$$"
tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/shared-ingress-smoke.XXXXXX")"
config_dir="${tmp_dir}/config"
runtime_dir="${tmp_dir}/runtime"

cleanup() {
	"${runtime}" rm -f "${container_name}" >/dev/null 2>&1 || true
	rm -rf "${tmp_dir}"
}
trap cleanup EXIT

if ! command -v curl >/dev/null 2>&1; then
	echo >&2 "curl is required to run the shared-ingress smoke test"
	exit 1
fi

if ! command -v python3 >/dev/null 2>&1; then
	echo >&2 "python3 is required to allocate a free host port"
	exit 1
fi

host_port="${HOST_PORT:-$(python3 - <<'PY'
import socket

sock = socket.socket()
sock.bind(("127.0.0.1", 0))
print(sock.getsockname()[1])
sock.close()
PY
)}"

mkdir -p "${config_dir}" "${runtime_dir}"

cat > "${config_dir}/haproxy.cfg" <<'EOF'
global
  log stdout local0

defaults
  mode tcp
  timeout connect 5s
  timeout client 30s
  timeout server 30s

frontend dataplane-kas-svc
  bind :::6443 v4v6
  default_backend no-match

frontend external-dns
  bind :::8443 v4v6
  default_backend no-match

listen health_check_http_url
  bind :::9444 v4v6
  mode http
  monitor-uri /haproxy_ready

backend no-match
  tcp-request content reject
EOF

echo "Building ${image_tag} with ${runtime}"
"${runtime}" build -f "${repo_root}/shared-ingress/Containerfile" -t "${image_tag}" "${repo_root}/shared-ingress" >/dev/null

echo "Starting shared-ingress smoke-test container on 127.0.0.1:${host_port}"
"${runtime}" run -d \
	--name "${container_name}" \
	--read-only \
	--publish "127.0.0.1:${host_port}:9444" \
	--volume "${config_dir}:/usr/local/etc/haproxy:ro" \
	--volume "${runtime_dir}:/var/run/haproxy" \
	--entrypoint haproxy \
	"${image_tag}" \
	-f /usr/local/etc/haproxy \
	-db \
	-W \
	-S /var/run/haproxy/admin.sock >/dev/null

attempt=0
until curl --fail --silent "http://127.0.0.1:${host_port}/haproxy_ready" >/dev/null; do
	attempt=$((attempt + 1))
	if [ "${attempt}" -ge 30 ]; then
		echo >&2 "HAProxy did not become ready within 30 seconds"
		"${runtime}" logs "${container_name}" >&2 || true
		exit 1
	fi
	sleep 1
done

if [ ! -S "${runtime_dir}/admin.sock" ]; then
	echo >&2 "HAProxy did not create /var/run/haproxy/admin.sock"
	"${runtime}" logs "${container_name}" >&2 || true
	exit 1
fi

state="$("${runtime}" inspect --format '{{.State.Status}}' "${container_name}")"
if [ "${state}" != "running" ]; then
	echo >&2 "Container is not running after readiness check: ${state}"
	"${runtime}" logs "${container_name}" >&2 || true
	exit 1
fi

echo "shared-ingress smoke test passed"
