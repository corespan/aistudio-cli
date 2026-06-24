#!/bin/bash
set -e

REPO="corespan/aistudio-cli"
BINARY_NAME="ai-studio-cli"

# 1. Get the latest release tag
TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
echo "Latest release: ${TAG}"

# 2. Download the tarball
TARBALL="${TAG}.tar.gz"
echo "Downloading ${TARBALL}..."
curl -fsSL "https://github.com/${REPO}/releases/download/${TAG}/${TARBALL}" -o "/tmp/${TARBALL}"

# 3. Extract and install
tar -xzf "/tmp/${TARBALL}" -C /tmp
chmod +x "/tmp/${BINARY_NAME}"
echo "Installing to /usr/local/bin/..."
sudo mv "/tmp/${BINARY_NAME}" "/usr/local/bin/${BINARY_NAME}"
rm -f "/tmp/${TARBALL}"

echo "Done. Run: ${BINARY_NAME} --help"
