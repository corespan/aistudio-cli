# ai-studio-cli

A command-line tool for node provisioning (CUDA toolkit, Nvidia recommended drivers, libraries) and GPU bandwidth benchmarking via `nvbandwidth`. See https://github.com/NVIDIA/nvbandwidth.

Supports all major NVIDIA GPU architectures including the **CoreSpan 5090 Inference System** (Blackwell / RTX 5090, GB202). The provisioning script automatically detects the RTX 5090, selects CUDA 12.8, and installs driver 570+.

## Building from Source

```bash
go mod tidy

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ai-studio-cli .
```

---

## Installation

After building, move the binary to `/usr/local/bin` to use it globally:

```bash
sudo mv ai-studio-cli /usr/local/bin/
```

---

## Node Setup

Setup is split into two phases. Phase 2 must be run after the reboot that Phase 1 triggers.

**Phase 1 — Install dependencies**

```bash
ai-studio-cli setup dependencies
```

> If the installation of drivers occurs, the system reboots at the end of Phase 1.

**Phase 2 — Build nvbandwidth**

Run this to verify drivers, clone `nvbandwidth` from GitHub, and compile it.

```bash
ai-studio-cli setup nvbandwidth
```

---

## GPU Bandwidth Benchmarking

`nvbandwidth` must be installed first via `setup nvbandwidth` above.

```bash
# List all available test cases
ai-studio-cli nvbandwidth list

# Run all test cases
ai-studio-cli nvbandwidth run

# Run a specific test case
ai-studio-cli nvbandwidth test host_to_device_memcpy_sm
```

#### Flags for `run` and `test`

| Flag | Short | Default | Description |
|---|---|---|---|
| `--buffer-size` | `-b` | `512` | Memcpy buffer size in MiB |
| `--samples` | `-i` | `3` | Number of benchmark iterations |
| `--verbose` | `-v` | | Enable verbose output |
| `--json` | `-j` | | Output results as JSON |
| `--skip-verify` | `-s` | | Skip data verification after copy |
| `--use-mean` | `-m` | | Use arithmetic mean instead of median |

---

## NVLink Benchmarking (CoreSpan 5090 Inference System)

The CoreSpan 5090 Inference System connects multiple RTX 5090 GPUs via NVLink. Use the dedicated `nvlink` subcommand to validate peer-to-peer interconnect health and bandwidth:

```bash
ai-studio-cli nvbandwidth nvlink
```

This runs six peer-to-peer testcases covering unidirectional and bidirectional NVLink transfers as well as all-to-host / host-to-all patterns.

```bash
# JSON output (for monitoring / alerting pipelines)
ai-studio-cli nvbandwidth nvlink --json

# Larger buffer and more samples for sustained-bandwidth measurement
ai-studio-cli nvbandwidth nvlink --buffer-size 1024 --samples 10
```

The same flags as `run` and `test` apply.

---

## Usage Notes

- **Sudo Password**: The tool will securely prompt for your sudo password interactively during setup.
---
