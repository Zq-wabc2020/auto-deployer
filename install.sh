#!/bin/sh
set -e

VERSION="${VERSION:-latest}"
INSTALL_DIR="/usr/local/bin"
HOST_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
HOST_ARCH=$(uname -m)

case "$HOST_ARCH" in
    x86_64) GOARCH="amd64" ;;
    aarch64|arm64) GOARCH="arm64" ;;
    *) echo "Unsupported architecture: $HOST_ARCH"; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
    URL="https://github.com/auto-deployer/auto-deployer/releases/latest/download/deployd-${HOST_OS}-${GOARCH}.tar.gz"
else
    URL="https://github.com/auto-deployer/auto-deployer/releases/download/${VERSION}/deployd-${HOST_OS}-${GOARCH}.tar.gz"
fi

echo "Downloading deployd ${VERSION} for ${HOST_OS}/${GOARCH}..."
curl -fsSL "$URL" | tar -xz -C /tmp
sudo mv /tmp/deployd-${HOST_OS}-${GOARCH} "${INSTALL_DIR}/deployd"
chmod +x "${INSTALL_DIR}/deployd"

echo "deployd installed to ${INSTALL_DIR}/deployd"
deployd --version
