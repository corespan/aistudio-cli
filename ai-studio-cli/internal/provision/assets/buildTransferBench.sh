#!/bin/bash
# Build AMD TransferBench from source against the node's ROCm install and put
# the binary beside ai-studio-cli.
#
#   buildTransferBench.sh <git-tag-or-branch> <install-dir> [<uid>:<gid>]
#
# Runs as root (via ai-studio-cli setup transferbench). Unlike the NVIDIA
# path, this does NOT install drivers: the amdgpu driver and ROCm must already
# be present. https://rocm.docs.amd.com/projects/install-on-linux/
set -euo pipefail

REF="${1:?usage: $0 <git-ref> <install-dir> [uid:gid]}"
INSTALL_DIR="${2:?usage: $0 <git-ref> <install-dir> [uid:gid]}"
OWNER="${3:-}"
ROCM_PATH="${ROCM_PATH:-/opt/rocm}"
REPO="https://github.com/ROCm/TransferBench.git"

echo "--- Checking ROCm ---"
if [ ! -e /dev/kfd ]; then
    echo "Error: /dev/kfd not found — the amdgpu kernel driver is not loaded."
    echo "  Install the AMD GPU driver and ROCm first:"
    echo "  https://rocm.docs.amd.com/projects/install-on-linux/"
    exit 1
fi
if [ ! -d "$ROCM_PATH" ]; then
    echo "Error: ROCm not found at $ROCM_PATH. Install ROCm, or set ROCM_PATH."
    exit 1
fi
# Same compiler search order as TransferBench's Makefile.
COMPILER=""
for c in bin/amdclang++ llvm/bin/amdclang++ llvm/bin/clang++ bin/hipcc; do
    if [ -x "$ROCM_PATH/$c" ]; then COMPILER="$ROCM_PATH/$c"; break; fi
done
if [ -z "$COMPILER" ]; then
    echo "Error: no HIP compiler under $ROCM_PATH (looked for amdclang++, clang++, hipcc)."
    echo "  Install the ROCm development packages (e.g. 'rocm-hip-sdk')."
    exit 1
fi
echo "ROCm:     $ROCM_PATH ($(cat "$ROCM_PATH/.info/version" 2>/dev/null || echo 'version unknown'))"
echo "Compiler: $COMPILER"

echo "--- Installing build dependencies ---"
if command -v apt-get &>/dev/null; then
    apt-get install -y -q git make libnuma-dev
elif command -v dnf &>/dev/null; then
    dnf install -y -q git make numactl-devel
elif [ ! -f /usr/include/numa.h ]; then
    echo "Error: libnuma headers not found and no apt-get/dnf to install them."
    exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

echo "--- Cloning TransferBench $REF ---"
git clone --quiet --depth 1 --branch "$REF" "$REPO" "$WORK/TransferBench"

echo "--- Building (GPU_TARGETS=native: for the GPUs in this node) ---"
make -C "$WORK/TransferBench" -j"$(nproc)" ROCM_PATH="$ROCM_PATH" TransferBench

install -m 0755 "$WORK/TransferBench/TransferBench" "$INSTALL_DIR/TransferBench"
if [ -n "$OWNER" ]; then
    chown "$OWNER" "$INSTALL_DIR/TransferBench"
fi
echo "Installed: $INSTALL_DIR/TransferBench"

echo "--- Detected topology ---"
OUTPUT_TO_CSV=0 "$INSTALL_DIR/TransferBench" 2>&1 | sed -n '/Detected/,$p' | head -40 || true
