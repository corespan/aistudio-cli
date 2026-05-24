# ai-studio-cli

[![License](https://img.shields.io/badge/license-Apache%202.0-blue.svg)](LICENSE)

A command-line tool for GPU node provisioning (CUDA toolkit, NVIDIA recommended drivers, container toolkit) and GPU memory bandwidth benchmarking via [`nvbandwidth`](https://github.com/NVIDIA/nvbandwidth).

---

## Prerequisites

- **OS**: Ubuntu 20.04, 22.04, or 24.04 (x86_64)
- **GPU**: NVIDIA GPU (Pascal, Volta, Turing, Ampere, Ada, Hopper, or Blackwell architecture)
- **Build**: Go 1.21+
- **Runtime**: `git`, `cmake`, `sudo` access

---

## Building from Source

```bash
cd ai-studio-cli
go mod tidy
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ai-studio-cli .
```

---

## Installation

After building, move the binary to `/usr/local/bin` to use it globally:

```bash
sudo mv ai-studio-cli /usr/local/bin/
```

Copy the `assets/` directory alongside the binary (required for setup):

```bash
sudo cp -r assets/ /usr/local/bin/assets/
```

---

## Node Setup

Setup is split into two phases. Phase 2 must be run after the reboot that Phase 1 may trigger.

### Phase 1 — Install dependencies

Installs the NVIDIA Container Toolkit, CUDA toolkit, NVIDIA drivers, and build tools (`cmake`, `build-essential`, etc.). Reboots automatically if new drivers or CUDA are installed.

```bash
ai-studio-cli setup dependencies
```

> The `assets/driversInstallation.sh` script must be present before running this command. The tool will prompt for your sudo password interactively.

### Phase 2 — Build nvbandwidth

Run after the reboot. Verifies drivers, clones [`nvbandwidth`](https://github.com/NVIDIA/nvbandwidth) from GitHub, compiles it, and places the binary next to `ai-studio-cli`.

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

# Run a specific test case by name
ai-studio-cli nvbandwidth test host_to_device_memcpy_sm
```

### Flags for `run` and `test`

| Flag | Short | Default | Description |
|---|---|---|---|
| `--buffer-size` | `-b` | `512` | Memcpy buffer size in MiB |
| `--samples` | `-i` | `3` | Number of benchmark iterations |
| `--duration` | `-d` | `0` | Duration of the test in seconds (if supported) |
| `--src` | | | Source device or node |
| `--dest` | | | Destination device or node |
| `--verbose` | `-v` | | Enable verbose output |
| `--json` | `-j` | | Output results as JSON |
| `--skip-verify` | `-s` | | Skip data verification after copy |
| `--use-mean` | `-m` | | Use arithmetic mean instead of median |

---

## Configuration

The CLI reads configuration from `~/.ai-studio-cli/config.yaml`. You can store your sudo password there to skip the interactive prompt:

```yaml
setup:
  sudo_password: "your-sudo-password"
```

A `--profile` flag is available on all commands to select a named config profile (default: `default`).

---

## Project Structure

```
ai-studio-cli/
├── main.go                         # Entry point
├── cmd/
│   ├── root.go                     # Root command and global flags
│   ├── setup.go                    # setup dependencies / nvbandwidth
│   └── nvbandwidth.go              # nvbandwidth list / run / test
├── internal/
│   ├── config/config.go            # Viper-based config (YAML + env vars)
│   ├── nvbandwidth/client.go       # nvbandwidth binary wrapper
│   └── provision/node.go           # Node provisioning logic
└── assets/
    └── driversInstallation.sh      # Phase 1 & 2 shell script
```

---

## License

Apache 2.0 — see [LICENSE](LICENSE).
