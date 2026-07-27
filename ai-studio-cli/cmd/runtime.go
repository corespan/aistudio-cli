package cmd

import (
	"os/exec"
	"strings"
)

// containerRuntime selects the container engine for vLLM/bench operations.
// Values: "auto" (default — detect installed engine), "docker", or "podman".
// It is bound to the --runtime flag on the bench, ab-bench, and vllm commands.
var containerRuntime = "auto"

// containerEngine describes how to invoke compose and the container CLI for a
// concrete runtime ("docker" or "podman").
//
// The two runtimes diverge in how compose is invoked and how GPUs are exposed:
//   - Docker uses the `docker compose` plugin and the NVIDIA container runtime.
//   - Podman uses the standalone `podman-compose` binary and CDI devices
//     (`nvidia.com/gpu=all`). Note: `podman compose` (the subcommand that
//     delegates to docker-compose) silently drops CDI devices, so we always
//     shell out to `podman-compose` instead.
type containerEngine struct {
	rt string // "docker" | "podman"
}

// currentEngine resolves the active engine from the --runtime flag, falling
// back to auto-detection (Docker preferred, then Podman).
func currentEngine() containerEngine {
	switch strings.ToLower(strings.TrimSpace(containerRuntime)) {
	case "podman":
		return containerEngine{rt: "podman"}
	case "docker":
		return containerEngine{rt: "docker"}
	default: // auto
		if commandAvailable("docker") {
			return containerEngine{rt: "docker"}
		}
		if commandAvailable("podman-compose") || commandAvailable("podman") {
			return containerEngine{rt: "podman"}
		}
		// Nothing detected — default to docker so callers surface a clear
		// "docker not found" error rather than a confusing one.
		return containerEngine{rt: "docker"}
	}
}

func commandAvailable(bin string) bool {
	_, err := exec.LookPath(bin)
	return err == nil
}

// cli returns the container CLI binary (docker/podman) used for run/pull/inspect.
func (e containerEngine) cli() string { return e.rt }

// isPodman reports whether this engine is Podman.
func (e containerEngine) isPodman() bool { return e.rt == "podman" }

// composeArgv returns the binary and full argument list for a compose
// subcommand against the given file.
func (e containerEngine) composeArgv(file string, args ...string) (string, []string) {
	if e.rt == "podman" {
		full := []string{}
		if file != "" {
			full = append(full, "-f", file)
		}
		return "podman-compose", append(full, args...)
	}
	full := []string{"compose"}
	if file != "" {
		full = append(full, "-f", file)
	}
	return "docker", append(full, args...)
}

// composeCommand builds an *exec.Cmd for a compose subcommand against file.
func (e containerEngine) composeCommand(file string, args ...string) *exec.Cmd {
	bin, full := e.composeArgv(file, args...)
	return exec.Command(bin, full...)
}

// composeDisplay returns a human-readable command string for logging.
func (e containerEngine) composeDisplay(file string, args ...string) string {
	bin, full := e.composeArgv(file, args...)
	return bin + " " + strings.Join(full, " ")
}

// composeAvailable reports whether the compose tooling for this engine is
// installed, returning a helpful message when it is not.
func (e containerEngine) composeAvailable() (bool, string) {
	if e.rt == "podman" {
		if !commandAvailable("podman-compose") {
			return false, "podman runtime selected but 'podman-compose' is not installed.\n" +
				"  Install it with 'pip install podman-compose' (or 'apt-get install podman-compose'),\n" +
				"  or provision with: ai-studio-cli setup vllm --runtime podman"
		}
		return true, ""
	}
	if !commandAvailable("docker") {
		return false, "docker runtime selected but 'docker' is not installed.\n" +
			"  Install it with: ai-studio-cli setup vllm"
	}
	return true, ""
}

// dockerGPUBlock and podmanGPUBlock are the GPU passthrough stanzas for the
// bundled compose file. Docker uses the compose `deploy.resources` reservation;
// podman-compose honours CDI devices instead. composeForRuntime swaps one for
// the other so the rest of the bundled compose file stays byte-identical.
const dockerGPUBlock = `    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: all
              capabilities: [gpu]`

const podmanGPUBlock = `    devices:
      - nvidia.com/gpu=all
    security_opt:
      - label=disable`

// composeForRuntime adapts the bundled compose bytes for the target runtime.
// For docker it is a no-op; for podman it rewrites the GPU block to the CDI
// device form that podman-compose understands.
func composeForRuntime(raw []byte, e containerEngine) []byte {
	if !e.isPodman() {
		return raw
	}
	return []byte(strings.Replace(string(raw), dockerGPUBlock, podmanGPUBlock, 1))
}
