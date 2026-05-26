#!/bin/bash
set -e

PHASE=$1

# Detect OS and set the OS_STR variable (e.g. ubuntu2004, ubuntu2204, ubuntu2404)
detect_os() {
    if [ -f /etc/os-release ]; then
        source /etc/os-release
        if [[ "$ID" != "ubuntu" ]]; then
            echo "Error: This script only supports Ubuntu."
            exit 1
        fi
        # Remove the dot (e.g. 22.04 -> 2204)
        OS_VER=$(echo $VERSION_ID | tr -d '.')
        OS_STR="ubuntu${OS_VER}"
    else
        echo "Error: Cannot determine OS. /etc/os-release missing."
        exit 1
    fi

    if [[ "$OS_STR" != "ubuntu2004" && "$OS_STR" != "ubuntu2204" && "$OS_STR" != "ubuntu2404" ]]; then
        echo "Error: This script supports Ubuntu 20.04, 22.04, and 24.04. Detected: $VERSION_ID"
        exit 1
    fi
}

# Detects GPU via lspci and sets package URLs based on Arch and OS
detect_gpu() {
    if ! command -v lspci &>/dev/null; then
        sudo -E apt-get install -y -q pciutils
    fi

    # Refresh PCI ID database
    if command -v update-pciids &>/dev/null; then
        sudo update-pciids &>/dev/null || true
    fi

    LSPCI_RAW=$(lspci | grep -i nvidia || true)
    if [ -z "$LSPCI_RAW" ]; then
        echo "Error: No NVIDIA GPU detected via lspci."
        exit 1
    fi

    echo "Detected NVIDIA device(s):"
    echo "$LSPCI_RAW" | sed 's/^/  /'

    GPU_NAME=$(echo "$LSPCI_RAW" | head -1 | sed 's/.*: //')

    CHIP=$(echo "$GPU_NAME" | grep -oP '\b[A-Z]{2}[0-9]{2,3}[A-Z]{0,3}\b' | head -1 || true)
    CHIP_PREFIX="${CHIP:0:2}"
    ARCH="unknown"

    case "$CHIP_PREFIX" in
        GP) ARCH="pascal"    ;;
        GV) ARCH="volta"     ;;
        TU) ARCH="turing"    ;;
        GA) ARCH="ampere"    ;;
        AD) ARCH="ada"       ;;
        GH) ARCH="hopper"    ;;
        GB) ARCH="blackwell" ;;
    esac

    # Product-name fallback
    if [ "$ARCH" = "unknown" ]; then
        GPU_UPPER=$(echo "$GPU_NAME" | tr '[:lower:]' '[:upper:]')
        if   echo "$GPU_UPPER" | grep -qE "P40|P100|P4 |P6000|PASCAL";                      then ARCH="pascal"
        elif echo "$GPU_UPPER" | grep -qE "V100|GV100|VOLTA|TITAN V";                       then ARCH="volta"
        elif echo "$GPU_UPPER" | grep -qE "T4 |RTX 20|TURING|QUADRO RTX [3-6]000[^0]";     then ARCH="turing"
        elif echo "$GPU_UPPER" | grep -qE "A100|A6000|A30 |A10 |RTX 30|AMPERE|RTX A[0-9]"; then ARCH="ampere"
        elif echo "$GPU_UPPER" | grep -qE "RTX 40|L40|ADA|RTX 6000 ADA";                   then ARCH="ada"
        elif echo "$GPU_UPPER" | grep -qE "H100|H200|HOPPER";                               then ARCH="hopper"
        elif echo "$GPU_UPPER" | grep -qE "RTX 5090|RTX 5080|RTX 5070|RTX 50|B100|B200|B300|GB202|BLACKWELL"; then ARCH="blackwell"
        fi
    fi

    if [ "$ARCH" = "unknown" ]; then
        echo "Error: Could not determine GPU architecture from: $GPU_NAME"
        echo "  Set manually and re-run: export ARCH=ampere"
        exit 1
    fi

    echo "  GPU:  $GPU_NAME"
    echo "  Arch: $ARCH"

    BASE_URL="https://developer.download.nvidia.com/compute/cuda"

    # Set versions dynamically using OS_STR
    case "$ARCH" in
        pascal|volta|turing|ampere|ada|hopper)
            CUDA_VERSION="12.6"
            CUDA_DEB="cuda-repo-${OS_STR}-12-6-local_12.6.2-560.35.03-1_amd64.deb"
            CUDA_DEB_URL="${BASE_URL}/12.6.2/local_installers/${CUDA_DEB}"
            CUDA_TOOLKIT_PKG="cuda-toolkit-12-6"
            CUDA_PATH="/usr/local/cuda-12.6"
            MIN_DRIVER_MAJOR=560
            ;;
        blackwell)
            CUDA_VERSION="12.8"
            CUDA_DEB="cuda-repo-${OS_STR}-12-8-local_12.8.0-570.86.10-1_amd64.deb"
            CUDA_DEB_URL="${BASE_URL}/12.8.0/local_installers/${CUDA_DEB}"
            CUDA_TOOLKIT_PKG="cuda-toolkit-12-8"
            CUDA_PATH="/usr/local/cuda-12.8"
            MIN_DRIVER_MAJOR=570
            ;;
    esac

    echo "  Target CUDA: $CUDA_VERSION  ($CUDA_PATH)"
    echo "  Min driver:  ${MIN_DRIVER_MAJOR}.x"
    echo ""
}

# -----------------------------
# Phase 1: Heavy Installation
# -----------------------------
if [ "$PHASE" == "--phase1" ]; then
    echo "--- Phase 1: System Installation ---"

    # Silence all apt and needrestart interactive prompts
    export DEBIAN_FRONTEND=noninteractive
    export NEEDRESTART_MODE=a
    export NEEDRESTART_SUSPEND=1

    NEEDS_REBOOT=false

    # Detect OS first, then GPU
    detect_os
    detect_gpu

    sudo -E apt-get update -q

    # NVIDIA Container Toolkit
    distribution=$(. /etc/os-release; echo $ID$VERSION_ID)
    curl -s -L https://nvidia.github.io/libnvidia-container/gpgkey \
        | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg --batch --yes
    curl -s -L https://nvidia.github.io/libnvidia-container/$distribution/libnvidia-container.list \
        | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#' \
        | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

    sudo -E apt-get update -q
    sudo -E apt-get install -y nvidia-container-toolkit

    # CUDA
    # Look specifically for the nvcc binary, not just an empty leftover directory
    if ls /usr/local/cuda-*/bin/nvcc 1> /dev/null 2>&1; then
        EXISTING_CUDA_BIN=$(ls /usr/local/cuda-*/bin/nvcc | sort -V | tail -1)
        EXISTING_CUDA=$(dirname $(dirname "$EXISTING_CUDA_BIN"))
        echo "Existing CUDA installation found ($EXISTING_CUDA) — skipping CUDA install."
    else
        echo "No valid CUDA installation found — installing CUDA $CUDA_VERSION..."
        NEEDS_REBOOT=true
        CUDA_PIN_URL="https://developer.download.nvidia.com/compute/cuda/repos/${OS_STR}/x86_64/cuda-${OS_STR}.pin"
        wget -q --show-progress "$CUDA_PIN_URL" -O cuda-${OS_STR}.pin
        sudo mv cuda-${OS_STR}.pin /etc/apt/preferences.d/cuda-repository-pin-600

        echo "Downloading $CUDA_DEB (~3 GB)..."
        wget -q --show-progress -c "$CUDA_DEB_URL" -O "$CUDA_DEB"
        sudo -E dpkg -i "$CUDA_DEB"
        sudo cp /var/cuda-repo-*/cuda-*-keyring.gpg /usr/share/keyrings/ 2>/dev/null || true
        sudo -E apt-get update -q
        sudo -E apt-get install -y "$CUDA_TOOLKIT_PKG"
        echo "CUDA $CUDA_VERSION installed at $CUDA_PATH"
    fi

    # Build dependencies for nvbandwidth
    sudo -E apt-get install -y build-essential cmake git libboost-program-options-dev

    # Driver
    if dpkg -l 2>/dev/null | grep -qE "^ii.*nvidia-driver-[0-9]+"; then
        EXISTING_DRV=$(dpkg -l | grep -E "^ii.*nvidia-driver-[0-9]+" | awk '{print $2}' | sort -V | tail -1)
        echo "Existing NVIDIA driver found ($EXISTING_DRV) — skipping driver install."
    else
        echo "No NVIDIA driver found — installing..."
        NEEDS_REBOOT=true
        sudo add-apt-repository -y ppa:graphics-drivers/ppa
        sudo -E apt-get update -q
        RECOMMENDED_DRIVER=$(ubuntu-drivers devices 2>/dev/null | grep 'recommended' | awk '{print $3}' || true)

        if [ -n "$RECOMMENDED_DRIVER" ]; then
            REC_VER=$(echo "$RECOMMENDED_DRIVER" | grep -oP '[0-9]+$' || echo "0")
            if [ "$REC_VER" -ge "$MIN_DRIVER_MAJOR" ]; then
                echo "Installing recommended driver: $RECOMMENDED_DRIVER"
                sudo -E apt-get install -y "$RECOMMENDED_DRIVER"
            else
                echo "Recommended driver ($REC_VER) is below minimum ($MIN_DRIVER_MAJOR) for $ARCH — installing nvidia-driver-${MIN_DRIVER_MAJOR} directly."
                sudo -E apt-get install -y "nvidia-driver-${MIN_DRIVER_MAJOR}"
            fi
        else
            echo "No recommended driver from ubuntu-drivers — installing nvidia-driver-${MIN_DRIVER_MAJOR}."
            sudo -E apt-get install -y "nvidia-driver-${MIN_DRIVER_MAJOR}"
        fi
        echo "Driver install complete."
    fi

    # Persist CUDA on PATH
    if [ ! -d "$CUDA_PATH" ]; then
        CUDA_PATH=$(dirname $(dirname $(ls /usr/local/cuda-*/bin/nvcc | sort -V | tail -1)))
    fi
    PROFILE_SNIPPET="/etc/profile.d/cuda-path.sh"
    if ! grep -q "${CUDA_PATH}" "$PROFILE_SNIPPET" 2>/dev/null; then
        sudo tee "$PROFILE_SNIPPET" > /dev/null <<EOF
export PATH="${CUDA_PATH}/bin:\$PATH"
export LD_LIBRARY_PATH="${CUDA_PATH}/lib64:\${LD_LIBRARY_PATH:-}"
EOF
        echo "CUDA path written to $PROFILE_SNIPPET"
    fi

    echo ""
    if [ "$NEEDS_REBOOT" = true ]; then
        echo "Phase 1 complete. New drivers/CUDA installed."
        echo "Rebooting in 10 seconds... (After reboot, run Phase 2)"
        sleep 10
        sudo reboot
    else
        echo "Phase 1 complete. All drivers and CUDA versions were already present."
        echo "Skipping reboot. You can proceed directly to Phase 2."
    fi
    exit 0
fi

# -----------------------------
# Phase 2: Post-Reboot Build
# -----------------------------
if [ "$PHASE" == "--phase2" ]; then
    echo "--- Phase 2: Building Tools ---"

    detect_os
    detect_gpu

    if [ ! -d "$CUDA_PATH" ]; then
        if ls /usr/local/cuda-*/bin/nvcc 1> /dev/null 2>&1; then
            CUDA_PATH=$(dirname $(dirname $(ls /usr/local/cuda-*/bin/nvcc | sort -V | tail -1)))
            echo "Note: Expected CUDA path not found; using $CUDA_PATH"
        else
            echo "Error: No CUDA installation found. Run Phase 1 first."
            exit 1
        fi
    fi

    export PATH="${CUDA_PATH}/bin:$PATH"
    export LD_LIBRARY_PATH="${CUDA_PATH}/lib64:${LD_LIBRARY_PATH:-}"

    if ! command -v nvcc &>/dev/null; then
        echo "Error: nvcc not found at ${CUDA_PATH}/bin/nvcc. CUDA install may have failed."
        exit 1
    fi
    echo "nvcc: $(nvcc --version | grep release)"

    if ! command -v nvidia-smi &>/dev/null; then
        echo "Error: nvidia-smi not found — driver did not load after reboot."
        echo "  Check: sudo dmesg | grep -iE 'nvidia|nvrm' | tail -20"
        exit 1
    fi
    echo "Driver: $(nvidia-smi --query-gpu=driver_version --format=csv,noheader | head -1)"

    if [ ! -d "./nvbandwidth" ]; then
        echo "Pulling nvbandwidth source from build server ${BUILD_SERVER_IP}..."
        if ! command -v sshpass &>/dev/null; then
            export DEBIAN_FRONTEND=noninteractive
            sudo -E apt-get install -y -q sshpass
        fi
        SSHPASS="${BUILD_SERVER_PASS}" sshpass -e scp -o StrictHostKeyChecking=no -r \
            "${BUILD_SERVER_USER}@${BUILD_SERVER_IP}:${BUILD_SERVER_DIR}/nvbandwidth" ./nvbandwidth
    fi

    if [ ! -d "./nvbandwidth" ]; then
        echo "Error: Failed to pull nvbandwidth directory from build server."
        exit 1
    fi

    cd ./nvbandwidth
    cmake . || { echo "Error: cmake configuration failed"; exit 1; }
    make -j"$(nproc)" || { echo "Error: make build failed"; exit 1; }
    echo "nvbandwidth build complete: $(pwd)/nvbandwidth"
    exit 0
fi

echo "Invalid usage. Use --phase1 or --phase2"
exit 1