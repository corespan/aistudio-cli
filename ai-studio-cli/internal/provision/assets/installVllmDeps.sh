#!/bin/bash
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a
export NEEDRESTART_SUSPEND=1

# Container runtime to install/configure: docker (default), podman, or both.
# Override via environment, e.g. CONTAINER_RUNTIME=podman
CONTAINER_RUNTIME="${CONTAINER_RUNTIME:-docker}"
RT=$(echo "$CONTAINER_RUNTIME" | tr '[:upper:]' '[:lower:]')

WANT_DOCKER=false
WANT_PODMAN=false
case "$RT" in
    docker)   WANT_DOCKER=true ;;
    podman)   WANT_PODMAN=true ;;
    both|all) WANT_DOCKER=true; WANT_PODMAN=true ;;
    *)
        echo ">>> Unknown CONTAINER_RUNTIME='$CONTAINER_RUNTIME' — defaulting to docker."
        WANT_DOCKER=true
        ;;
esac

echo ">>> vLLM dependency setup for container runtime: $RT"

# Ensure lspci is available for GPU detection.
if ! command -v lspci > /dev/null 2>&1; then
    sudo -E apt-get update
    sudo -E apt-get install -y pciutils
fi

HAS_GPU=false
if lspci 2>/dev/null | grep -qi nvidia; then
    HAS_GPU=true
fi

# Install the NVIDIA Container Toolkit. Shared by Docker and Podman — the
# package is identical; only the runtime configuration differs.
install_nvidia_toolkit() {
    if dpkg -l nvidia-container-toolkit 2>/dev/null | grep -q "^ii"; then
        echo "nvidia-container-toolkit is already installed. Skipping."
        nvidia-container-cli --version || true
        return 0
    fi

    echo "Installing nvidia-container-toolkit..."
    local distribution
    distribution=$(. /etc/os-release; echo "$ID$VERSION_ID")

    curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
        | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg

    curl -fsSL "https://nvidia.github.io/libnvidia-container/${distribution}/libnvidia-container.list" \
        | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#' \
        | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list > /dev/null

    sudo -E apt-get update
    sudo -E apt-get install -y nvidia-container-toolkit
    nvidia-container-cli --version || true
}

# ------------------------------------------------------------------
# Docker
# ------------------------------------------------------------------
setup_docker() {
    local NVIDIA_DOCKER2_INSTALLED=false
    local DOCKER_INSTALLED=false

    if dpkg -l nvidia-docker2 2>/dev/null | grep -q "^ii"; then
        NVIDIA_DOCKER2_INSTALLED=true
        DOCKER_INSTALLED=true
        echo ">>> nvidia-docker2 detected — Docker and GPU runtime are already configured."
        echo "    IMPORTANT: Your compose file must include 'runtime: nvidia' at the"
        echo "    service level to pass GPUs through."
    elif command -v docker > /dev/null 2>&1; then
        DOCKER_INSTALLED=true
        echo ">>> Standard Docker detected (no nvidia-docker2)."
    fi

    if [ "$DOCKER_INSTALLED" = "true" ]; then
        echo "Docker is already installed. Skipping Docker installation."
        docker --version
    else
        echo "Installing Docker Engine..."
        sudo -E apt-get install -y apt-transport-https ca-certificates curl software-properties-common lshw

        curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
            | sudo gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg

        echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] \
https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" \
            | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

        sudo -E apt-get update
        sudo -E apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

        echo "Docker installed:"
        docker --version
    fi

    # GPU runtime setup for Docker
    if [ "$NVIDIA_DOCKER2_INSTALLED" = "true" ]; then
        echo "Skipping nvidia-container-toolkit (nvidia-docker2 already provides GPU runtime)."
    elif [ "$HAS_GPU" = "true" ]; then
        install_nvidia_toolkit
        echo "Configuring nvidia-container-toolkit for Docker..."
        sudo nvidia-ctk runtime configure --runtime=docker
        sudo systemctl restart docker

        echo ""
        echo "    Use the compose deploy syntax in your compose file:"
        echo ""
        echo "      services:"
        echo "        vllm:"
        echo "          deploy:"
        echo "            resources:"
        echo "              reservations:"
        echo "                devices:"
        echo "                  - driver: nvidia"
        echo "                    count: all"
        echo "                    capabilities: [gpu]"
        echo ""
    else
        echo "No NVIDIA GPU detected. Skipping Docker GPU runtime setup."
    fi
}

# ------------------------------------------------------------------
# Podman
# ------------------------------------------------------------------
setup_podman() {
    if command -v podman > /dev/null 2>&1; then
        echo "Podman already installed: $(podman --version)"
    else
        echo "Installing Podman..."
        sudo -E apt-get update
        sudo -E apt-get install -y podman
        podman --version
    fi

    # podman-compose is what honours CDI devices; 'podman compose' (the
    # docker-compose shim) silently drops them. Install best-effort.
    if ! command -v podman-compose > /dev/null 2>&1; then
        echo "Installing podman-compose..."
        sudo -E apt-get install -y podman-compose \
            || echo "  Note: podman-compose unavailable via apt — install with 'pip install podman-compose'."
    fi

    if [ "$HAS_GPU" = "true" ]; then
        install_nvidia_toolkit

        echo "Generating CDI spec for Podman GPU access..."
        sudo mkdir -p /etc/cdi
        if sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml; then
            echo "CDI spec written to /etc/cdi/nvidia.yaml"
        else
            echo "Warning: 'nvidia-ctk cdi generate' failed — ensure the NVIDIA driver is loaded, then re-run:"
            echo "  sudo nvidia-ctk cdi generate --output=/etc/cdi/nvidia.yaml"
        fi

        # Auto-regenerate the spec after driver upgrades / reboot when available.
        if systemctl list-unit-files 2>/dev/null | grep -q '^nvidia-cdi-refresh.path'; then
            sudo systemctl enable --now nvidia-cdi-refresh.path nvidia-cdi-refresh.service 2>/dev/null || true
            echo "Enabled nvidia-cdi-refresh (auto-updates the CDI spec on driver changes)."
        fi

        echo ""
        echo "    Use the CDI device syntax in your compose file (podman-compose):"
        echo ""
        echo "      services:"
        echo "        vllm:"
        echo "          devices:"
        echo "            - nvidia.com/gpu=all"
        echo "          security_opt:"
        echo "            - label=disable"
        echo ""
        echo "    Run standalone containers with: podman run --device nvidia.com/gpu=all ..."
        echo ""
    else
        echo "No NVIDIA GPU detected. Skipping Podman GPU (CDI) setup."
    fi
}

if [ "$WANT_DOCKER" = "true" ]; then
    setup_docker
fi

if [ "$WANT_PODMAN" = "true" ]; then
    setup_podman
fi

# ------------------------------------------------------------------
# Apache Bench (shared)
# ------------------------------------------------------------------
if command -v ab > /dev/null 2>&1; then
    echo "Apache Bench (ab) is already installed. Skipping."
    ab -V 2>&1 | head -1
else
    echo "Installing Apache Bench (apache2-utils)..."
    sudo -E apt-get install -y apache2-utils
    echo "Apache Bench installed:"
    ab -V 2>&1 | head -1
fi
