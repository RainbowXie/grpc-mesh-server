#!/usr/bin/env bash
set -euo pipefail

# ------------------------------------------------------------------------------
# gen-token.sh
#
# Helper script for generating secure authentication tokens for wa-emu nodes.
# Generates cryptographically secure random tokens that can be used in both
# grpc-mesh-server (config.yaml) and grpc-mesh-node (reverse_gateway_config.json).
# ------------------------------------------------------------------------------

PREFIX="${PREFIX:-waemu_}"
COUNT="${COUNT:-1}"
LENGTH="${LENGTH:-40}"

info() {
  echo "[token] $*"
}

require_binary() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Error: missing dependency: $1" >&2
    echo "Please install OpenSSL to generate secure tokens." >&2
    exit 1
  fi
}

generate_token() {
  # Generate random bytes, base64 encode, make URL-safe, trim to length
  local raw
  raw=$(openssl rand -base64 64 | tr '+/' '-_' | tr -d '=' | tr -d '\n')
  local token="${raw:0:$LENGTH}"
  echo "${PREFIX}${token}"
}

show_usage() {
  cat <<EOF
Usage: $0 [options]

Generate secure authentication tokens for wa-emu nodes.

Options:
  -c, --count N      Generate N tokens (default: 1)
  -l, --length N     Token length without prefix (default: 40)
  -p, --prefix STR   Token prefix (default: waemu_)
  -h, --help         Show this help message

Environment Variables:
  COUNT              Same as --count
  LENGTH             Same as --length
  PREFIX             Same as --prefix

Examples:
  # Generate a single token
  ./scripts/gen-token.sh

  # Generate 5 tokens
  ./scripts/gen-token.sh -c 5

  # Generate token with custom prefix and length
  ./scripts/gen-token.sh -p "myapp_" -l 32

  # Generate tokens without prefix
  PREFIX="" ./scripts/gen-token.sh

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    -c|--count)
      COUNT="$2"
      shift 2
      ;;
    -l|--length)
      LENGTH="$2"
      shift 2
      ;;
    -p|--prefix)
      PREFIX="$2"
      shift 2
      ;;
    -h|--help)
      show_usage
      exit 0
      ;;
    *)
      echo "Error: Unknown option: $1" >&2
      echo "Run with --help for usage information." >&2
      exit 1
      ;;
  esac
done

# Validate inputs
if ! [[ "$COUNT" =~ ^[0-9]+$ ]] || [ "$COUNT" -lt 1 ]; then
  echo "Error: COUNT must be a positive integer" >&2
  exit 1
fi

if ! [[ "$LENGTH" =~ ^[0-9]+$ ]] || [ "$LENGTH" -lt 16 ]; then
  echo "Error: LENGTH must be at least 16" >&2
  exit 1
fi

require_binary openssl

info "Generating $COUNT token(s) with prefix '$PREFIX' and length $LENGTH..."
echo ""

for ((i=1; i<=COUNT; i++)); do
  token=$(generate_token)
  if [ "$COUNT" -gt 1 ]; then
    printf "[%2d] %s\n" "$i" "$token"
  else
    echo "$token"
  fi
done

echo ""
info "Tokens generated successfully!"
echo ""
info "Next steps:"
echo ""
echo "1. Add tokens to grpc-mesh-server config (config/config.yaml):"
echo "   security:"
echo "     allowed_tokens:"
echo "       - \"<token>\""
echo ""
echo "2. Add token to grpc-mesh-node config (config/reverse_gateway_config.json):"
echo "   \"node\": {"
echo "     \"token\": \"<token>\""
echo "   }"
echo ""
echo "3. Or set via environment variable:"
echo "   export WA_NODE_TOKEN=\"<token>\""
echo ""
info "See docs/token_policy.md for more information."
