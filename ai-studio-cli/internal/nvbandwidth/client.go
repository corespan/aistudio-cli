package nvbandwidth

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type Runner struct {
	BufferSize  int
	TestSamples int
	Duration    int
	Src         string
	Dest        string
	Verbose     bool
	JSON        bool
	SkipVerify  bool
	UseMean     bool
}

func NewRunner() *Runner {
	return &Runner{
		BufferSize:  512,
		TestSamples: 3,
	}
}

func (r *Runner) Run(baseArgs []string) error {
	bin := r.findBinary()

	extraFlags := r.buildFlags()
	args := make([]string, len(baseArgs), len(baseArgs)+len(extraFlags))
	copy(args, baseArgs)
	args = append(args, extraFlags...)

	c := exec.Command(bin, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr

	if err := c.Run(); err != nil {
		return fmt.Errorf("nvbandwidth failed: %w", err)
	}
	return nil
}

func (r *Runner) findBinary() string {
	name := "nvbandwidth"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), name)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return name
}

func (r *Runner) buildFlags() []string {
	var flags []string
	if r.BufferSize != 512 {
		flags = append(flags, "--bufferSize", fmt.Sprintf("%d", r.BufferSize))
	}
	if r.TestSamples != 3 {
		flags = append(flags, "--testSamples", fmt.Sprintf("%d", r.TestSamples))
	}
	if r.Duration > 0 {
		flags = append(flags, "--duration", fmt.Sprintf("%d", r.Duration))
	}
	if r.Src != "" {
		flags = append(flags, "--src", r.Src)
	}
	if r.Dest != "" {
		flags = append(flags, "--dest", r.Dest)
	}
	if r.Verbose {
		flags = append(flags, "--verbose")
	}
	if r.JSON {
		flags = append(flags, "--json")
	}
	if r.SkipVerify {
		flags = append(flags, "--skipVerification")
	}
	if r.UseMean {
		flags = append(flags, "--useMean")
	}
	return flags
}
