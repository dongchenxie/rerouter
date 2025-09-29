#!/usr/bin/env bash
# Install latest (or specified) Go on Linux to /usr/local, and set PATH.
# Usage: curl -fsSL https://raw.githubusercontent.com/<your-repo>/scripts/install-go-linux.sh | bash
# Or locally: chmod +x scripts/install-go-linux.sh && ./scripts/install-go-linux.sh

set -euo pipefail

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  armv7l) ARCH=armv6l ;;
  *) echo "Unsupported arch: $ARCH" >&2; exit 1 ;;
esac

if [[ "$OS" != "linux" ]]; then
  echo "This script supports Linux only. For macOS use Homebrew: brew install go" >&2
  exit 1
fi

GO_VERSION=${GO_VERSION:-}
if [[ -z "${GO_VERSION}" ]]; then
  echo "Detecting latest Go version..."
  GO_VERSION=$(curl -fsSL https://go.dev/VERSION?m=text | head -n1)
fi

TARBALL="${GO_VERSION}.${OS}-${ARCH}.tar.gz"
URL="https://go.dev/dl/${TARBALL}"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "Downloading ${URL} ..."
curl -fL "${URL}" -o "$TMP/go.tgz"

echo "Installing to /usr/local (requires sudo)..."
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "$TMP/go.tgz"

# Ensure PATH setup for bash/zsh
append_line_if_missing() {
  local file="$1"; shift
  local line="$*"
  [[ -f "$file" ]] || touch "$file"
  if ! grep -Fqs "$line" "$file"; then
    printf '\n%s\n' "$line" >> "$file"
  fi
}

append_line_if_missing "$HOME/.bashrc" 'export PATH="/usr/local/go/bin:$PATH"'
append_line_if_missing "$HOME/.zshrc"  'export PATH="/usr/local/go/bin:$PATH"'
append_line_if_missing "$HOME/.bashrc" 'export GOPATH="$HOME/go"'
append_line_if_missing "$HOME/.zshrc"  'export GOPATH="$HOME/go"'
append_line_if_missing "$HOME/.bashrc" 'export GOBIN="$GOPATH/bin"'
append_line_if_missing "$HOME/.zshrc"  'export GOBIN="$GOPATH/bin"'
append_line_if_missing "$HOME/.bashrc" 'export PATH="$GOBIN:$PATH"'
append_line_if_missing "$HOME/.zshrc"  'export PATH="$GOBIN:$PATH"'

mkdir -p "$HOME/go/bin"

echo
echo "Go installed: $($(/usr/local/go/bin/go) version)"
echo "Shell config updated. Open a new terminal or run one of:"
echo "  source ~/.bashrc    # for bash"
echo "  source ~/.zshrc     # for zsh"
echo
echo "Then verify: go version"
echo "Finally, run this app:"
echo "  cd $(pwd)"
echo "  B_BASE_URL=https://your-b-site.example.com go run ."

