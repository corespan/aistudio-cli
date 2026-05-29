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

**Phase 3 — Install vLLM Dependencies (Optional)**

If you intend to run inference benchmarks using the Docker fallback strategy, you must install Docker Engine and the NVIDIA Container Toolkit.

```bash
ai-studio-cli setup vllm
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

## Inference Benchmarking (vLLM)

The `bench` subcommand provides an automated way for testing LLM serving performance (Throughput, TTFT, TPOT) using `vllm`.

### Step 1: Install the dependencies

Run the following command to install the dependencies required to setup the vLLM server:

```bash
ai-studio-cli setup vllm
```

### Step 2: Start the vLLM Server

Before running the benchmarks, you need to bring up the actual inference server. Since you already installed the dependencies (`ai-studio-cli setup vllm`), the easiest and most reliable way to do this is using Docker Compose to bring up the server. You can use this command:
```bash
ai-studio-cli vllm up --compose-file docker-compose.yaml
```

**3. Manage and Monitor**  
You can use these commands to manage your local inference environment:
```bash
# Check if the server is online and see which models are loaded
ai-studio-cli vllm status

# Follow the live inference logs
ai-studio-cli vllm logs -f

# Shut down the server and free up GPU memory
ai-studio-cli vllm down
```

> **Bring Your Own Server:** You don't have to use our Docker setup! If you already have vLLM running natively or on another machine, simply skip the `vllm up` steps and pass the `--endpoint http://<ip>:<port>` flag directly to the `bench` commands below.

### 1. Benchmark using Synthetic/Random Tokens (Default)
If you don't provide a dataset, the tool defaults to the `random` dataset. It will automatically generate synthetic gibberish requests based on your token length flags. This is the best way to test maximum theoretical hardware throughput.
```bash
ai-studio-cli bench --endpoint http://<ip>:<port> --input-len 512 --max-tokens 256 --concurrency 64
```

### 2. Benchmark using a Custom Prompt
Provide an exact string to test. The CLI will automatically generate a temporary dataset behind the scenes, repeating your prompt for the requested amount.
```bash
ai-studio-cli bench --endpoint http://<ip>:<port> --prompt "Explain the concept of machine learning." --requests 100 --concurrency 10
```

### Hybrid Execution Strategy

The benchmarking tool automatically determines the best way to run the test:
1. **Local Execution**: If `vllm` is installed locally in your Python environment, it will run the benchmark natively to avoid overhead.
2. **Docker Fallback**: If `vllm` is missing (common on fresh nodes), it automatically falls back to pulling and running the `vllm/vllm-openai:latest` Docker image. The Docker container uses host networking to reach your inference server.

You can force Docker execution at any time using `--force-docker`.

### Auto-Detection & Structured Results

Results are automatically saved to a structured directory in the following format:
`bench-results/{model}/{gpu_tag}/{timestamp}/benchmark_result.json`

- **Model Name**: If you don't specify `--model`, the tool hits the `/v1/models` endpoint of your inference server to auto-detect the served model.
- **GPU Tag**: The tool reads `nvidia-smi` to auto-detect your hardware (e.g., `A100-SXM4-80GB`). You can override this with `--gpu-tag`.

#### Flags for `bench`

| Flag | Default | Description |
|---|---|---|
| `--endpoint` | `http://[IP_ADDRESS]:[PORT]` | Address of the vLLM OpenAI-compatible server |
| `--model` | *(auto-detected)* | Name of the model to request |
| `--dataset` | `random` | Dataset type: `random`, `sharegpt`, `custom` |
| `--dataset-path`| | Path to dataset JSON (if using `sharegpt`) |
| `--prompt` | | Single custom prompt string to use |
| `--requests` | `1000` | Number of requests to process |
| `--concurrency` | `10` | Number of concurrent requests |
| `--gpu-tag` | *(auto-detected)* | Override the detected GPU tag for the results folder |

---

## Usage Notes

- **Sudo Password**: The tool will securely prompt for your sudo password interactively during setup.
---
