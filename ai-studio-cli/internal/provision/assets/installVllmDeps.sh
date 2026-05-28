#!/bin/bash
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a
export NEEDRESTART_SUSPEND=1

# Detect existing Docker / NVIDIA-Docker setup
NVIDIA_DOCKER2_INSTALLED=false
DOCKER_INSTALLED=false

if dpkg -l nvidia-docker2 2>/dev/null | grep -q "^ii"; then
    NVIDIA_DOCKER2_INSTALLED=true
    DOCKER_INSTALLED=true
    echo ">>> nvidia-docker2 detected — Docker and GPU runtime are already configured."
    echo "    IMPORTANT: Your docker-compose.yml must include 'runtime: nvidia' at the"
    echo "    service level to pass GPUs through. Example:"
    echo ""
    echo "      services:"
    echo "        vllm:"
    echo "          runtime: nvidia"
    echo "          environment:"
    echo "            - NVIDIA_VISIBLE_DEVICES=all"
    echo ""
elif command -v docker > /dev/null 2>&1; then
    DOCKER_INSTALLED=true
    echo ">>> Standard Docker detected (no nvidia-docker2)."
fi

# Install Docker (if missing)
if [ "$DOCKER_INSTALLED" = "true" ]; then
    echo "Docker is already installed. Skipping Docker installation."
    docker --version
else
    echo "Installing Docker Engine..."

    sudo -E apt-get install -y apt-transport-https ca-certificates curl software-properties-common lshw

    # Add Docker's official GPG key
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
        | sudo gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg

    # Add Docker's official APT repository
    echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] \
https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" \
        | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

    sudo -E apt-get update
    # Install latest stable Docker
    sudo -E apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

    echo "Docker installed:"
    docker --version
fi

# Gpu runtime setup 
if [ "$NVIDIA_DOCKER2_INSTALLED" = "true" ]; then
    echo "Skipping nvidia-container-toolkit (nvidia-docker2 already provides GPU runtime)."
elif lshw -C display 2>/dev/null | grep -qi nvidia || lspci 2>/dev/null | grep -qi nvidia; then
    if dpkg -l nvidia-container-toolkit 2>/dev/null | grep -q "^ii"; then
        echo "nvidia-container-toolkit is already installed. Skipping."
        nvidia-container-cli --version
    else
        echo "NVIDIA GPU detected. Installing nvidia-container-toolkit..."

        distribution=$(. /etc/os-release; echo "$ID$VERSION_ID")

        curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey \
            | sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg

        curl -fsSL "https://nvidia.github.io/libnvidia-container/${distribution}/libnvidia-container.list" \
            | sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#' \
            | sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list > /dev/null

        sudo -E apt-get update
        sudo -E apt-get install -y nvidia-container-toolkit

        echo "Configuring nvidia-container-toolkit for Docker..."
        sudo nvidia-ctk runtime configure --runtime=docker

        sudo systemctl restart docker

        echo "NVIDIA Container Toolkit installed:"
        nvidia-container-cli --version
    fi

    echo ""
    echo "    Use the compose v3 deploy syntax in your docker-compose.yml:"
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
    echo "    NOTE: 'runtime: nvidia' is the nvidia-docker2 approach and is NOT"
    echo "    recommended with nvidia-container-toolkit — use deploy.resources instead."
    echo ""
else
    echo "No NVIDIA GPU detected. Skipping GPU runtime setup."
fi

if command -v ab > /dev/null 2>&1; then
    echo "Apache Bench (ab) is already installed. Skipping."
    ab -V 2>&1 | head -1
else
    echo "Installing Apache Bench (apache2-utils)..."
    sudo -E apt-get install -y apache2-utils
    echo "Apache Bench installed:"
    ab -V 2>&1 | head -1
fi

