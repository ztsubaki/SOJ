#!/usr/bin/env bash
set -Eeuo pipefail

generate_bff_tokens() {
  # These credentials authorize only loopback BFF-to-Control-Plane RPCs. They
  # are ephemeral bearer secrets, so randomness—not encryption or persistence—
  # is the required property.
  export WEB_BFF_SERVICE_TOKEN
  export ADMIN_BFF_SERVICE_TOKEN
  WEB_BFF_SERVICE_TOKEN="$(openssl rand -hex 32)"
  ADMIN_BFF_SERVICE_TOKEN="$(openssl rand -hex 32)"
}

prepare_grpc_certificate() {
  local certificate_dir=/var/lib/soj/tls
  local ca_file="${certificate_dir}/ca.crt"
  local ca_key_file="${certificate_dir}/ca.key"
  local certificate_file="${certificate_dir}/grpc.crt"
  local key_file="${certificate_dir}/grpc.key"
  local certificate_request="${certificate_dir}/grpc.csr"

  if [[ ! -d "${certificate_dir}" ]]; then
    install -d -m 0700 "${certificate_dir}"
  fi
  if [[ -e "${ca_file}" || -e "${certificate_file}" || -e "${key_file}" ]]; then
    if [[ ! -f "${ca_file}" || ! -f "${certificate_file}" || ! -f "${key_file}" ]]; then
      echo "${ca_file}, ${certificate_file}, and ${key_file} must either all exist or all be absent" >&2
      exit 1
    fi
  else
    echo "generating Control Plane gRPC CA and server certificate" >&2
    umask 077
    openssl req -x509 -newkey rsa:3072 -nodes -sha256 \
      -days 365 \
      -keyout "${ca_key_file}" \
      -out "${ca_file}" \
      -subj '/CN=SOJ Control Plane local CA' \
      -addext 'basicConstraints=critical,CA:TRUE' \
      -addext 'keyUsage=critical,keyCertSign,cRLSign' \
      >/dev/null 2>&1
    openssl req -newkey rsa:3072 -nodes -sha256 \
      -keyout "${key_file}" \
      -out "${certificate_request}" \
      -subj '/CN=control-plane.internal' \
      >/dev/null 2>&1
    openssl x509 -req \
      -in "${certificate_request}" \
      -CA "${ca_file}" \
      -CAkey "${ca_key_file}" \
      -CAcreateserial \
      -out "${certificate_file}" \
      -days 365 \
      -sha256 \
      -extfile <(printf '%s\n' \
        'subjectAltName=DNS:control-plane.internal,DNS:localhost,IP:127.0.0.1' \
        'basicConstraints=critical,CA:FALSE' \
        'keyUsage=critical,digitalSignature,keyEncipherment' \
        'extendedKeyUsage=serverAuth') \
      >/dev/null 2>&1
    rm -f "${ca_key_file}" "${certificate_request}" "${certificate_dir}/ca.srl"
  fi

  # These are process-local wiring, not user configuration. The BFF and
  # Control Plane always consume the fixed files from the certificate volume.
  export GRPC_TLS_CERT_FILE="${certificate_file}"
  export GRPC_TLS_KEY_FILE="${key_file}"
  export GRPC_TLS_CA_FILE="${ca_file}"
}

generate_bff_tokens
prepare_grpc_certificate

# The BFFs connect to the loopback client address above. The Control Plane
# listener can still be exposed to an externally deployed Judge Agent.
GRPC_ADDR=:50051 /usr/local/bin/soj-api &
api_pid=$!

(
  cd /app/apps/web
  exec /app/node_modules/.bin/next start -p 3000
) &
web_pid=$!

(
  cd /app/apps/admin
  exec /app/node_modules/.bin/next start -p 3001
) &
admin_pid=$!

pids=("${api_pid}" "${web_pid}" "${admin_pid}")

stop() {
  trap - TERM INT
  kill -TERM "${pids[@]}" 2>/dev/null || true
  wait "${pids[@]}" 2>/dev/null || true
}

trap 'stop; exit 0' TERM INT

# If any service exits unexpectedly, stop the others so orchestration can
# restart a single unhealthy container instead of leaving a partial stack up.
if wait -n "${pids[@]}"; then
  status=0
else
  status=$?
fi
stop
exit "${status}"
