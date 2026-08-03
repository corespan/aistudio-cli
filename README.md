# ai-studio-cli

A command-line tool for node provisioning (CUDA toolkit, Nvidia recommended drivers, libraries) and GPU bandwidth benchmarking via `nvbandwidth`. See https://github.com/NVIDIA/nvbandwidth.

Supports all major NVIDIA GPU architectures including the **CoreSpan 5090 Inference System** (Blackwell / RTX 5090, GB202). The provisioning script automatically detects the RTX 5090, selects CUDA 12.8, and installs driver 570+.

## Installation

### Quick Install (from GitHub Releases)

```bash
curl -sL https://raw.githubusercontent.com/corespan/aistudio-cli/master/install.sh | bash
```

This downloads the latest release binary for your platform and installs it to `/usr/local/bin`. Requires `curl`, `tar`, and `sudo`.

### Building from Source

```bash
cd ai-studio-cli

go mod tidy

CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o ai-studio-cli .

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

**Choosing a container runtime (Docker / Podman)**

Phase 1 installs the NVIDIA Container Toolkit and configures GPU access for the
container runtime of your choice. Select it with `--runtime`:

```bash
# Docker (default) — configures the NVIDIA container runtime
ai-studio-cli setup dependencies --runtime docker

# Podman — generates a CDI spec (/etc/cdi/nvidia.yaml) and installs podman
ai-studio-cli setup dependencies --runtime podman

# Both
ai-studio-cli setup dependencies --runtime both
```

The toolkit package is identical for both; only the access mechanism differs —
Docker uses the NVIDIA container runtime, while Podman uses the Container Device
Interface (CDI). With Podman, run GPU containers via
`podman run --device nvidia.com/gpu=all ...`. The CDI spec is regenerated
automatically after driver updates/reboots when the `nvidia-cdi-refresh`
service is available (NVIDIA Container Toolkit ≥ 1.18).

**Phase 2 — Build nvbandwidth**

Run this to verify drivers, clone `nvbandwidth` from GitHub, and compile it.

```bash
ai-studio-cli setup nvbandwidth
```

**Phase 3 — Install vLLM Dependencies (Optional)**

If you intend to run inference benchmarks using the container fallback strategy, you must install a container runtime and the NVIDIA Container Toolkit. Choose the runtime with `--runtime` (default `docker`):

```bash
# Docker (default)
ai-studio-cli setup vllm

# Podman — installs podman + podman-compose and generates a CDI spec
ai-studio-cli setup vllm --runtime podman

# Both
ai-studio-cli setup vllm --runtime both
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

### Quick Start — Zero-Config Benchmark

The CLI comes bundled with a default, optimized compose file (configured for `Qwen2.5-32B-Instruct` with FP8 and PP-4 and TP-1). If you don't provide a compose file, it will use this default automatically:

```bash
# Simplest case — uses the built-in default compose
ai-studio-cli bench --requests 200 --concurrency 20 --max-tokens 1024

# With Apache Bench
ai-studio-cli ab-bench --requests 200 --concurrency 20 --max-tokens 1024
```

#### Container runtime (Docker or Podman)

The `bench`, `ab-bench`, and `vllm` commands work with either runtime. By default the runtime is **auto-detected** (Docker preferred, then Podman); override it with `--runtime`:

```bash
# Force Podman — uses podman-compose and CDI device passthrough
ai-studio-cli bench --runtime podman --requests 200 --concurrency 20
```

Under Podman the bundled compose file is automatically adapted (the GPU `deploy.resources` reservation is rewritten to the CDI `devices: [nvidia.com/gpu=all]` form, which `podman-compose` understands). If you supply your *own* `--compose-file` for Podman, use the CDI device syntax yourself.

To keep the server running after the benchmark (e.g., for follow-up runs):
```bash
ai-studio-cli bench --keep-server --requests 200
```

> **Note:** If the server is already running on the endpoint, the CLI detects it and skips the compose-up step automatically.

### Custom Compose File (Override)

If you need full control over the Docker Compose configuration, provide your own file:
```bash
ai-studio-cli bench --compose-file docker-compose.yaml --requests 200 --concurrency 20
```

### Prerequisites (One-Time Setup)

Before running benchmarks with the built-in vLLM server, ensure you have a container runtime (**Docker Engine** or **Podman** + `podman-compose`) and the **NVIDIA Container Toolkit** installed. Run `ai-studio-cli setup vllm --runtime <docker|podman|both>` to install them. 

### Manual Server Management

If you prefer to manage the vLLM server separately:
```bash
# Start the server
ai-studio-cli vllm up --compose-file docker-compose.yaml

# Check status
ai-studio-cli vllm status

# View logs
ai-studio-cli vllm logs -f

# Stop the server
ai-studio-cli vllm down
```

> **Bring Your Own Server:** If you already have vLLM running natively or on another machine, skip the compose steps entirely and pass `--endpoint http://<ip>:<port>` directly to the `bench` commands.

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

### 3. Benchmark using a Real Dataset (ShareGPT)
For realistic testing, you can download this standard dataset available:

**Setup — Download the standard dataset:**
HuggingFace now requires a free account token to download this dataset. Ensure your `HF_TOKEN` is exported, then run:

```bash
wget --header="Authorization: Bearer $HF_TOKEN" https://huggingface.co/datasets/anon8231489123/ShareGPT_Vicuna_unfiltered/resolve/main/ShareGPT_V3_unfiltered_cleaned_split.json
```

**Run the benchmark:**
```bash
ai-studio-cli bench --endpoint http://<ip>:<port> --dataset sharegpt --dataset-path ./ShareGPT_V3_unfiltered_cleaned_split.json --requests 1000 --concurrency 32
```

### Hybrid Execution Strategy

The benchmarking tool automatically determines the best way to run the test:
1. **Local Execution**: If `vllm` is installed locally in your Python environment (e.g., via `pip install vllm`), it will run the benchmark natively to avoid overhead.
2. **Container Fallback**: If `vllm` is missing (common on fresh nodes), it automatically falls back to pulling and running the `vllm/vllm-openai:latest` image with the active runtime (Docker or Podman, per `--runtime`). The benchmark client container uses host networking to reach your inference server (no GPU is requested — the client is a plain load generator).

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
| `--compose-file`| | Custom docker-compose.yml; overrides the built-in default |
| `--keep-server` | `false` | Leave the vLLM server running after the benchmark |
| `--server-timeout`| `900` | Seconds to wait for the vLLM server to become ready |

---

### Benchmark Dashboard (`bench-ui`)

A built-in web dashboard for visualising and comparing benchmark results. The entire UI is embedded in the binary — no additional files or dependencies needed.

```bash

# Launch the dashboard
ai-studio-cli bench-ui --port 8050 --open

# Point to a different results directory
ai-studio-cli bench-ui --result-dir /path/to/bench-results
```

The dashboard provides:
- **KPI cards** — Total throughput, output throughput, TTFT, and TPOT at a glance
- **Performance trend chart** — Visualise metrics across runs with configurable Y-axis
- **Model config bar** — Shows TP/PP, quantization, dtype, max model length, GPU memory utilization
- **Filterable table** — Filter by model, GPU, and precision; search across all runs
- **Sidebar navigation** — Quickly switch between benchmark runs

#### Flags for `bench-ui`

| Flag | Default | Description |
|---|---|---|
| `--port` | `9090` | HTTP listen port for the dashboard |
| `--result-dir` | `bench-results` | Root of the structured benchmark results directory |
| `--open` | `false` | Automatically open the dashboard in the default browser |

---

## Usage Notes

- **Sudo Password**: The tool will securely prompt for your sudo password interactively during setup.
---

---

## Licensing

CoreSpan AI's source in this repository is licensed **Apache-2.0** — see
[`LICENSE`](LICENSE) and [`NOTICE`](NOTICE).

Apache-2.0 Section 6 grants no trademark rights. "CoreSpan" and the CoreSpan
logo are trademarks of CoreSpan AI.

### Third-party attribution

Go statically links its dependencies, so a released `ai-studio-cli` binary
contains compiled copies of every module it builds against, plus the web UI
assets embedded via `go:embed`. Distributing the binary distributes all of it,
and MIT, BSD-3 and Apache-2.0 all require the copyright notice to travel with a
distributed copy.

Unlike an interpreted project there is no `node_modules` or `site-packages`
beside the artifact for those notices to live in — the binary is the whole
distribution — so they are compiled into it:

```bash
ai-studio-cli licenses              # everything embedded in this binary
ai-studio-cli licenses cobra        # filter to one dependency
```

The same text is published as a release asset, so it can be read without
running the binary.

### What this tool installs but does not distribute

`ai-studio-cli` provisions NVIDIA drivers, the CUDA userspace, Docker or Podman,
vLLM and container images onto GPU nodes. CoreSpan does not distribute those —
they are downloaded by you, from their publishers, onto your machines, under
their terms. The NVIDIA driver and CUDA EULAs in particular are between you and
NVIDIA. Nothing in our Apache-2.0 grant extends to them.

### Verifying it yourself

```bash
make compliance      # licence files, embedded notices, no CDN refs in the UI
make notices-check   # embedded notices match the real module graph (needs Go)
make notices         # regenerate them after a dependency change
make vendor-ui       # re-vendor the bench UI fonts and Chart.js
```

CI runs all of these plus a build that asserts `ai-studio-cli licenses` prints
real notices. See [`.github/workflows/compliance.yml`](.github/workflows/compliance.yml).
