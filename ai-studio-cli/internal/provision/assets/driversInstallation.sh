#!/bin/bash
set -e

PHASE=$1

# Container runtime to enable for GPU access: docker (default), podman, or both.
# Override via environment, e.g. CONTAINER_RUNTIME=podman
CONTAINER_RUNTIME="${CONTAINER_RUNTIME:-docker}"

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

# Check for kernel module vs userspace library version mismatch.
# Must be called after detect_gpu so MIN_DRIVER_MAJOR is set.
check_driver_userspace_match() {
    RUNFILE_DRIVER_LOADED=false

    if [ ! -f /proc/driver/nvidia/version ]; then
        # No runfile driver loaded — nothing to check here
        return 0
    fi

    # Full version string from the kernel module e.g. "580.142"
    RUNFILE_VER=$(grep -oP 'Kernel Module\s+\K[0-9]+\.[0-9]+' /proc/driver/nvidia/version | head -1 || echo "0")
    RUNFILE_VER_MAJOR=$(echo "$RUNFILE_VER" | cut -d. -f1)

    echo "Runfile-installed NVIDIA driver found (version $RUNFILE_VER)."

    # --- Minimum version check ---
    if [ "$RUNFILE_VER_MAJOR" -lt "$MIN_DRIVER_MAJOR" ]; then
        echo "Error: Runfile driver $RUNFILE_VER does not meet minimum $MIN_DRIVER_MAJOR for $ARCH."
        echo "  Remove it first: sudo /usr/bin/nvidia-uninstall"
        echo "  Then re-run this script."
        exit 1
    fi

    # --- Userspace library version check ---
    # Find the versioned libnvidia-ml on disk.
    USERSPACE_LIB=$(find /usr/lib/x86_64-linux-gnu /usr/lib -maxdepth 1 \
        -name "libnvidia-ml.so.[0-9]*.[0-9]*" 2>/dev/null | sort -V | tail -1 || true)

    if [ -n "$USERSPACE_LIB" ]; then
        # Extract version from filename e.g. libnvidia-ml.so.580.159.03 -> 580.159
        USERSPACE_VER=$(basename "$USERSPACE_LIB" | grep -oP '[0-9]+\.[0-9]+' | head -1 || echo "")

        if [ -n "$USERSPACE_VER" ] && [ "$USERSPACE_VER" != "$RUNFILE_VER" ]; then
            echo ""
            echo "Error: NVIDIA driver version mismatch detected."
            echo "  Kernel module (runfile) : $RUNFILE_VER"
            echo "  Userspace libraries     : $USERSPACE_VER"
            echo ""
            echo "  nvidia-smi and any NVML-based tool will fail until this is resolved."
            echo "  Fix options:"
            echo ""
            echo "  Option A — Update the runfile kernel module to match userspace libs:"
            echo "    wget https://us.download.nvidia.com/XFree86/Linux-x86_64/${USERSPACE_VER}/NVIDIA-Linux-x86_64-${USERSPACE_VER}.run"
            echo "    sudo sh NVIDIA-Linux-x86_64-${USERSPACE_VER}.run --silent"
            echo "    sudo reboot"
            echo ""
            echo "  Option B — Remove apt NVIDIA packages and reinstall at the runfile version:"
            echo "    sudo apt-get remove --purge 'libnvidia-*' 'libcuda*' 'nvidia-*' -y"
            echo "    sudo apt-get autoremove -y"
            echo "    sudo ldconfig"
            echo "    Then re-run this script."
            echo ""
            exit 1
        fi
    fi

    echo "  Driver version matches userspace libraries — OK."
    RUNFILE_DRIVER_LOADED=true
}

# Configure GPU access for the selected container runtime(s).
#   docker -> NVIDIA container runtime via 'nvidia-ctk runtime configure'
#   podman -> Container Device Interface (CDI) spec via 'nvidia-ctk cdi generate'
# Must be called AFTER the NVIDIA driver is installed and loaded so that CDI
# generation can inspect the live driver. Assumes nvidia-container-toolkit is
# already installed (it is, earlier in Phase 1).
setup_container_runtime() {
    local rt
    rt=$(echo "$CONTAINER_RUNTIME" | tr '[:upper:]' '[:lower:]')

    local want_docker=false
    local want_podman=false
    case "$rt" in
        docker)   want_docker=true ;;
        podman)   want_podman=true ;;
        both|all) want_docker=true; want_podman=true ;;
        *)
            echo "Warning: unknown CONTAINER_RUNTIME='$CONTAINER_RUNTIME' — defaulting to docker."
            want_docker=true
            ;;
    esac

    echo ""
    echo "--- Configuring GPU container runtime: $rt ---"

    # --- Podman path (CDI) ---
    if [ "$want_podman" = true ]; then
        if ! command -v podman &>/dev/null; then
            echo "Installing Podman..."
            sudo -E apt-get install -y podman
        else
            echo "Podman already installed: $(podman --version)"
        fi

        # CDI GPU passthrough needs Podman >= 4.1; Ubuntu 20.04 ships 3.4.x.
        PODMAN_VER=$(podman --version 2>/dev/null | grep -oE '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || true)
        PODMAN_MAJOR=$(echo "$PODMAN_VER" | cut -d. -f1)
        if [ -n "$PODMAN_MAJOR" ] && [ "$PODMAN_MAJOR" -lt 4 ]; then
            echo "WARNING: Podman $PODMAN_VER is too old for CDI GPU passthrough (needs >= 4.1)."
            echo "  'podman run --device nvidia.com/gpu=all' will not work — upgrade Podman or use Docker."
        fi

        # podman-compose honours CDI devices ('podman compose' drops them). It
        # is not packaged on older Ubuntu, so fall back to pip3 when needed.
        if ! command -v podman-compose &>/dev/null; then
            echo "Installing podman-compose..."
            if sudo -E apt-get install -y podman-compose 2>/dev/null; then
                echo "podman-compose installed via apt."
            else
                if ! command -v pip3 &>/dev/null; then
                    sudo -E apt-get install -y python3-pip
                fi
                # PEP 668 (Ubuntu 23.04+): retry with --break-system-packages.
                sudo -E pip3 install podman-compose 2>/dev/null \
                    || sudo -E pip3 install --break-system-packages podman-compose \
                    || echo "  Note: could not install podman-compose; install manually: sudo pip3 install --break-system-packages podman-compose"
            fi
        fi

        # Podman accesses GPUs through a CDI spec, not the Docker runtime hook.
        sudo mkdir -p /etc/cdi
        if sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml; then
            echo "CDI spec written to /etc/cdi/nvidia.yaml"
            echo "  Run GPU containers with: podman run --device nvidia.com/gpu=all ..."
        else
            echo "Warning: 'nvidia-ctk cdi generate' failed (driver may not be fully loaded yet)."
            echo "  Regenerate after reboot: sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml"
        fi

        # Toolkit >= 1.18 ships a service that regenerates the CDI spec after
        # driver upgrades and on reboot (the spec hard-codes driver paths).
        # Enable it when present; it is a no-op on older toolkits.
        if systemctl list-unit-files 2>/dev/null | grep -q '^nvidia-cdi-refresh.path'; then
            sudo systemctl enable --now nvidia-cdi-refresh.path nvidia-cdi-refresh.service 2>/dev/null || true
            echo "Enabled nvidia-cdi-refresh (auto-updates the CDI spec on driver changes)."
        fi
    fi

    # --- Docker path (NVIDIA container runtime) ---
    if [ "$want_docker" = true ]; then
        if command -v docker &>/dev/null; then
            echo "Configuring Docker to use the NVIDIA container runtime..."
            sudo nvidia-ctk runtime configure --runtime=docker
            sudo systemctl restart docker 2>/dev/null || true
            echo "Docker configured. Use the compose 'deploy.resources.reservations.devices' GPU syntax."
        else
            echo "Docker is not installed yet — its NVIDIA runtime will be configured"
            echo "  when you run: ai-studio-cli setup vllm"
        fi
    fi
    echo ""
}

# -----------------------------
# Phase 1: Heavy Installation
# -----------------------------
if [ "$PHASE" == "--phase1" ]; then
    echo "--- Phase 1: System Installation ---"

    # Block on desktop environments
    if systemctl is-active --quiet gdm3 2>/dev/null || \
       systemctl is-active --quiet lightdm 2>/dev/null || \
       systemctl is-active --quiet sddm 2>/dev/null; then
        echo "Error: A display manager is running on this machine."
        echo "  This script is intended for headless servers only."
        echo "  Do not run this on a desktop Ubuntu installation."
        exit 1
    fi

    # Silence all apt and needrestart interactive prompts
    export DEBIAN_FRONTEND=noninteractive
    export NEEDRESTART_MODE=a
    export NEEDRESTART_SUSPEND=1

    NEEDS_REBOOT=false

    # Detect OS and GPU first — sets ARCH, MIN_DRIVER_MAJOR, CUDA_* vars
    detect_os
    detect_gpu

    # FIX: Check for kernel/userspace mismatch BEFORE touching anything.
    check_driver_userspace_match

    sudo -E apt-get update -q

    # NVIDIA Container Toolkit
    # Install only after confirming no version mismatch exists.
    distribution=$(. /etc/os-release; echo $ID$VERSION_ID)
    curl -s -L https://nvidia.github.io/libnvidia-container/gpgkey \
        | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg --batch --yes
    curl -s -L https://nvidia.github.io/libnvidia-container/$distribution/libnvidia-container.list \
        | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#' \
        | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

    sudo -E apt-get update -q

    if [ "$RUNFILE_DRIVER_LOADED" = true ]; then
        # Mark all installed nvidia userspace packages as held so the
        # container toolkit install cannot pull in a newer/mismatched version
        HELD_PKGS=$(dpkg -l 2>/dev/null \
            | grep -E "^ii.*(libnvidia|libcuda)[^-]" \
            | awk '{print $2}' || true)
        if [ -n "$HELD_PKGS" ]; then
            echo "Holding nvidia userspace packages to prevent version drift:"
            echo "$HELD_PKGS" | sed 's/^/  /'
            echo "$HELD_PKGS" | xargs sudo apt-mark hold
        fi

        sudo -E apt-get install -y nvidia-container-toolkit

        # Release holds after toolkit install
        if [ -n "$HELD_PKGS" ]; then
            echo "$HELD_PKGS" | xargs sudo apt-mark unhold
        fi

        # Re-check after toolkit install — catches the edge case where the
        # machine had a runfile driver but no apt nvidia libs yet, meaning
        # HELD_PKGS was empty and apt was free to pull in any version
        echo "Verifying no version drift introduced by toolkit install..."
        check_driver_userspace_match
    else
        sudo -E apt-get install -y nvidia-container-toolkit
    fi

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

    # Check if Secure Boot is enabled.
    SECURE_BOOT_ON=false
    SECURE_BOOT_UNKNOWN=false

    if [ -d /sys/firmware/efi ]; then
        # UEFI boot confirmed — try reading the SecureBoot EFI variable directly
        SB_VAR=$(find /sys/firmware/efi/efivars -name "SecureBoot-*" 2>/dev/null | head -1)
        if [ -n "$SB_VAR" ]; then
            SB_STATE=$(od -An -t u1 "$SB_VAR" 2>/dev/null | awk '{print $NF}')
            if [ "$SB_STATE" = "1" ]; then
                SECURE_BOOT_ON=true
            fi
        else
            if command -v mokutil &>/dev/null; then
                MOKUTIL_OUT=$(mokutil --sb-state 2>/dev/null || true)
                if echo "$MOKUTIL_OUT" | grep -q "SecureBoot enabled"; then
                    SECURE_BOOT_ON=true
                elif echo "$MOKUTIL_OUT" | grep -qi "not supported\|not available\|EFI variables"; then
                    echo "Warning: Cannot determine Secure Boot state (EFI variables not accessible)."
                    echo "  If this is a bare-metal UEFI machine, verify Secure Boot is OFF in BIOS/UEFI"
                    echo "  before continuing, or the driver install may cause a kernel panic on reboot."
                    SECURE_BOOT_UNKNOWN=true
                fi
            else
                echo "Warning: mokutil is not installed and EFI variables are not accessible."
                echo "  Cannot verify Secure Boot state."
                echo "  If this is a bare-metal UEFI machine, verify Secure Boot is OFF in BIOS/UEFI"
                echo "  before continuing, or the driver install may cause a kernel panic on reboot."
                SECURE_BOOT_UNKNOWN=true
            fi
        fi
    else
        echo "  (Legacy BIOS boot detected — Secure Boot not applicable)"
    fi

    if [ "$SECURE_BOOT_ON" = true ]; then
        echo "Error: Secure Boot is enabled on this machine."
        echo "  NVIDIA kernel modules installed via apt are not signed and will be"
        echo "  rejected by the kernel on reboot, causing a kernel panic."
        echo "  Disable Secure Boot in BIOS/UEFI and re-run this script."
        exit 1
    fi

    if [ "$SECURE_BOOT_UNKNOWN" = true ]; then
        echo "Proceeding with driver install — verify Secure Boot is disabled manually if on bare metal."
    fi

    # RUNFILE_DRIVER_LOADED is already set by check_driver_userspace_match above
    if [ "$RUNFILE_DRIVER_LOADED" = false ]; then

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

            # Verify DKMS build succeeded and module is loadable before rebooting
            RUNNING_KERNEL=$(uname -r)
            if ! dkms status 2>/dev/null | grep -qi "nvidia.*${RUNNING_KERNEL}.*installed"; then
                echo "Error: NVIDIA DKMS module did not build successfully."
                echo "  Check: sudo dkms status"
                NVIDIA_DKMS_VER=$(dkms status 2>/dev/null | grep -i nvidia | grep -oP '[0-9]+\.[0-9]+\.[0-9]+' | head -1 || echo "")
                if [ -n "$NVIDIA_DKMS_VER" ]; then
                    echo "  Build log: /var/lib/dkms/nvidia/${NVIDIA_DKMS_VER}/build/make.log"
                fi
                exit 1
            fi

            sudo modprobe nvidia 2>/dev/null || true
            if ! lsmod | grep -q "^nvidia "; then
                echo "Error: NVIDIA kernel module failed to load in the current session."
                echo "  Rebooting now would cause a kernel panic."
                echo "  Check: sudo dmesg | grep -iE 'nvidia|nvrm' | tail -20"
                echo "  Check: sudo dkms status"
                exit 1
            fi

            if ! nvidia-smi &>/dev/null; then
                echo "Error: nvidia module loaded but nvidia-smi failed — driver may be partially broken."
                echo "  Check: nvidia-smi"
                exit 1
            fi

            echo "Driver install complete. Module loaded and verified."
        fi
    fi

    # Configure GPU access for the chosen container runtime(s). Done here,
    # after the driver is confirmed loaded, so CDI generation can succeed.
    setup_container_runtime

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

    # Verify no mismatch before attempting to build — nvbandwidth will
    # fail at runtime if the driver/library state is broken.
    check_driver_userspace_match

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

    # nvidia-smi is present — verify it actually works (catches NVML mismatch)
    if ! nvidia-smi &>/dev/null; then
        echo "Error: nvidia-smi failed — possible driver/library version mismatch."
        echo "  Run: nvidia-smi"
        echo "  And check: cat /proc/driver/nvidia/version"
        exit 1
    fi

    echo "Driver: $(nvidia-smi --query-gpu=driver_version --format=csv,noheader | head -1)"

    # nvbandwidth is cloned from NVIDIA's public GitHub repository.
    if [ ! -d "./nvbandwidth" ]; then
        echo "nvbandwidth directory not found — cloning from GitHub..."
        git clone https://github.com/NVIDIA/nvbandwidth.git ./nvbandwidth
    fi

    if [ ! -d "./nvbandwidth" ]; then
        echo "Error: Failed to clone nvbandwidth from GitHub."
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