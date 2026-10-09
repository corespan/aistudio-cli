// Package transferbench wraps AMD's TransferBench (https://github.com/ROCm/TransferBench),
// the ROCm counterpart to nvbandwidth: copy bandwidth between host and GPU
// memory, executed by GPU kernels (GFX), DMA engines or CPU threads.
//
// Unlike nvbandwidth, TransferBench takes almost no flags. The command line is
// `TransferBench <preset|config-file|cmdline> [bytes] [expression...]` and
// everything else — iterations, CSV output, per-preset options — is read from
// environment variables. The Runner therefore builds an environment as well as
// an argument list.
package transferbench

import (
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

// BinaryName is the executable the TransferBench Makefile produces.
const BinaryName = "TransferBench"

type Runner struct {
	// Bytes per Transfer, passed as TransferBench's second argument. Accepts
	// TransferBench's own K/M/G suffixes (binary multiples). Empty uses
	// TransferBench's default of 256M; "0" sweeps a range of sizes.
	Bytes string
	// Iterations maps to NUM_ITERATIONS. 0 keeps TransferBench's default (10);
	// a negative value runs for that many seconds instead.
	Iterations int
	// Warmups maps to NUM_WARMUPS. Empty leaves it unset, so TransferBench or
	// the preset picks (3 for most, 0 for smoketest). A string so that "unset"
	// and "0" stay distinct.
	Warmups string
	// NumGPUs maps to NUM_GPU_DEVICES: presets use the first NumGPUs GPUs.
	// 0 uses every detected GPU.
	NumGPUs int
	// CSV sets OUTPUT_TO_CSV=1. TransferBench has no JSON output.
	CSV bool
	// Env holds extra KEY=VALUE pairs passed straight through, for the many
	// preset-specific variables (`TransferBench envvars` lists them).
	Env []string
}

func NewRunner() *Runner {
	return &Runner{}
}

// Command builds the TransferBench invocation without running it.
//
// target is a preset name, a config file path, or "cmdline"/"dryrun" followed
// by a transfer expression in expr. presetEnv carries preset-specific settings
// chosen by a subcommand; r.Env is applied after it so the user can override.
func (r *Runner) Command(target string, expr []string, presetEnv map[string]string) (*exec.Cmd, error) {
	args, err := r.args(target, expr)
	if err != nil {
		return nil, err
	}
	env, err := r.environ(presetEnv)
	if err != nil {
		return nil, err
	}
	c := exec.Command(r.findBinary(), args...)
	c.Env = append(os.Environ(), env...)
	return c, nil
}

// Run executes TransferBench, streaming its output.
func (r *Runner) Run(target string, expr []string, presetEnv map[string]string) error {
	c, err := r.Command(target, expr, presetEnv)
	if err != nil {
		return err
	}
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		if _, ok := err.(*exec.Error); ok {
			return fmt.Errorf("TransferBench not found: %w\n  Install it with: ai-studio-cli setup transferbench", err)
		}
		return fmt.Errorf("TransferBench failed: %w", err)
	}
	return nil
}

func (r *Runner) args(target string, expr []string) ([]string, error) {
	if target == "" {
		return nil, nil // no arguments: TransferBench prints version, usage and topology
	}
	args := []string{target}
	if target == "cmdline" || target == "dryrun" {
		if len(expr) == 0 {
			return nil, fmt.Errorf("%s needs a transfer expression, e.g. \"1 4 (G0->G0->G1)\"", target)
		}
		// The expression starts at argv[3], so the size is not optional here.
		bytes := r.Bytes
		if bytes == "" {
			bytes = "256M"
		}
		return append(append(args, bytes), expr...), nil
	}
	if r.Bytes != "" {
		args = append(args, r.Bytes)
	}
	return args, nil
}

// environ returns the KEY=VALUE pairs to add to the process environment, in a
// stable order: runner settings, then preset settings, then user overrides.
func (r *Runner) environ(presetEnv map[string]string) ([]string, error) {
	var env []string
	if r.Iterations != 0 {
		env = append(env, fmt.Sprintf("NUM_ITERATIONS=%d", r.Iterations))
	}
	if r.Warmups != "" {
		n, err := strconv.Atoi(r.Warmups)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("invalid --warmups %q: expected a non-negative integer", r.Warmups)
		}
		env = append(env, fmt.Sprintf("NUM_WARMUPS=%d", n))
	}
	if r.NumGPUs > 0 {
		env = append(env, fmt.Sprintf("NUM_GPU_DEVICES=%d", r.NumGPUs))
	}
	if r.CSV {
		env = append(env, "OUTPUT_TO_CSV=1")
	}
	for _, k := range slices.Sorted(maps.Keys(presetEnv)) {
		env = append(env, k+"="+presetEnv[k])
	}
	for _, kv := range r.Env {
		if k, _, ok := strings.Cut(kv, "="); !ok || k == "" {
			return nil, fmt.Errorf("invalid --env %q: expected KEY=VALUE", kv)
		}
		env = append(env, kv)
	}
	return env, nil
}

// findBinary prefers a TransferBench beside ai-studio-cli (where
// `setup transferbench` puts it), then PATH, then the ROCm install tree.
func (r *Runner) findBinary() string {
	name := BinaryName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		if p := filepath.Join(filepath.Dir(exe), name); isFile(p) {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	rocm := os.Getenv("ROCM_PATH")
	if rocm == "" {
		rocm = "/opt/rocm"
	}
	if p := filepath.Join(rocm, "bin", name); isFile(p) {
		return p
	}
	return name
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
