#!/usr/bin/env bash
set -euo pipefail

# ------------------------------------------------------------------------------
# gen-dev-certs.sh
#
# Helper script for generating a self-signed CA and a server certificate/key
# pair for grpc-mesh-server development and testing. The generated artifacts land
# under config/tls/ by default and can be consumed directly by config.yaml.
# ------------------------------------------------------------------------------

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROJECT_ROOT="$(cd "${ROOT_DIR}/.." && pwd)"
TLS_DIR="${ROOT_DIR}/config/tls"
CA_KEY="${TLS_DIR}/ca.key"
CA_CRT="${TLS_DIR}/ca.crt"
SERVER_KEY="${TLS_DIR}/server.key"
SERVER_CSR="${TLS_DIR}/server.csr"
SERVER_CRT="${TLS_DIR}/server.crt"
DAYS="${DAYS:-365}"
CN="${CN:-grpc-mesh-server.local}"
SAN_DNS="${SAN_DNS:-}"
SAN_IPS="${SAN_IPS:-}"

# Target paths for CA certificate distribution
RUST_CONFIG_DIR="${PROJECT_ROOT}/grpc-mesh-node/config"
RUST_CA_CRT="${RUST_CONFIG_DIR}/ca.crt"

mkdir -p "${TLS_DIR}"

info() {
  echo "[certs] $*"
}

require_binary() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing dependency: $1" >&2
    exit 1
  fi
}

require_binary openssl

info "Generating CA key (${CA_KEY})"
openssl genrsa -out "${CA_KEY}" 4096 >/dev/null 2>&1

info "Generating CA certificate (${CA_CRT})"
openssl req -x509 -new -nodes \
  -key "${CA_KEY}" \
  -sha256 \
  -days "${DAYS}" \
  -subj "/CN=${CN} Root CA" \
  -out "${CA_CRT}"

info "Generating server key (${SERVER_KEY})"
openssl genrsa -out "${SERVER_KEY}" 4096 >/dev/null 2>&1

info "Generating server CSR (${SERVER_CSR})"
openssl req -new \
  -key "${SERVER_KEY}" \
  -subj "/CN=${CN}" \
  -out "${SERVER_CSR}"

TMP_EXT="$(mktemp)"

# Build SAN entries dynamically
SAN_ENTRIES="DNS.1 = ${CN}
DNS.2 = localhost
IP.1 = 127.0.0.1"

# Add custom DNS names
if [ -n "${SAN_DNS}" ]; then
  DNS_INDEX=3
  IFS=',' read -ra DNS_ARRAY <<< "${SAN_DNS}"
  for dns in "${DNS_ARRAY[@]}"; do
    dns_trimmed=$(echo "$dns" | xargs)
    if [ -n "$dns_trimmed" ]; then
      SAN_ENTRIES="${SAN_ENTRIES}
DNS.${DNS_INDEX} = ${dns_trimmed}"
      DNS_INDEX=$((DNS_INDEX + 1))
    fi
  done
fi

# Add custom IP addresses
if [ -n "${SAN_IPS}" ]; then
  IP_INDEX=2
  IFS=',' read -ra IP_ARRAY <<< "${SAN_IPS}"
  for ip in "${IP_ARRAY[@]}"; do
    ip_trimmed=$(echo "$ip" | xargs)
    if [ -n "$ip_trimmed" ]; then
      SAN_ENTRIES="${SAN_ENTRIES}
IP.${IP_INDEX} = ${ip_trimmed}"
      IP_INDEX=$((IP_INDEX + 1))
    fi
  done
fi

cat > "${TMP_EXT}" <<EOF
authorityKeyIdentifier=keyid,issuer
basicConstraints=CA:false
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth, clientAuth
subjectAltName = @alt_names

[alt_names]
${SAN_ENTRIES}
EOF

info "Signing server certificate (${SERVER_CRT})"
openssl x509 -req \
  -in "${SERVER_CSR}" \
  -CA "${CA_CRT}" \
  -CAkey "${CA_KEY}" \
  -CAcreateserial \
  -out "${SERVER_CRT}" \
  -days "${DAYS}" \
  -sha256 \
  -extfile "${TMP_EXT}"

rm -f "${SERVER_CSR}" "${TMP_EXT}" "${TLS_DIR}/ca.srl"

info "Done! Files generated in config/tls:"
ls -1 "${TLS_DIR}"

echo ""
info "Certificate includes SAN entries:"
openssl x509 -in "${SERVER_CRT}" -noout -text | grep -A 10 "Subject Alternative Name" | head -11

echo ""
info "Distributing CA certificate to dependent modules..."

# Copy CA certificate to grpc-mesh-node config directory
if [ -d "${RUST_CONFIG_DIR}" ]; then
  cp "${CA_CRT}" "${RUST_CA_CRT}"
  info "✓ Copied CA cert to: grpc-mesh-node/config/ca.crt"
else
  info "⚠ Skipping grpc-mesh-node: directory not found (${RUST_CONFIG_DIR})"
fi

echo ""
info "Certificate generation complete!"
info "CA Certificate: ${CA_CRT}"
info "Server Key:     ${SERVER_KEY}"
info "Server Cert:    ${SERVER_CRT}"
echo ""
info "To use these certificates:"
info "  1. Go server will read from: config/tls/"
info "  2. Rust client will use embedded: grpc-mesh-node/config/ca.crt"
info "  3. Rebuild grpc-mesh-node to embed the new CA certificate"
echo ""
info "Next steps:"
info "  cd ../grpc-mesh-node && cargo build --release --bin reverse_gateway"
