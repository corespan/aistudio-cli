package provision

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

//go:embed assets/driversInstallation.sh
var driversInstallScript []byte

//go:embed assets/installVllmDeps.sh
var installVllmDepsScript []byte

const nvbandwidthRepo = "https://github.com/NVIDIA/nvbandwidth.git"

type Provisioner struct {
    sudoPassword []byte
    // containerRuntime selects the GPU container runtime to configure in the
    // setup script: "docker" (default), "podman", or "both". Empty means leave
    // the script's own default in place.
    containerRuntime string
}

func NewProvisioner(sudoPass []byte) *Provisioner {
	pw := make([]byte, len(sudoPass))
	copy(pw, sudoPass)
	return &Provisioner{sudoPassword: pw}
}

func (p *Provisioner) Close() {
	for i := range p.sudoPassword {
		p.sudoPassword[i] = 0
	}
	p.sudoPassword = nil
}

// SetupLocal runs Phase 1. runtime selects the GPU container runtime to
// configure: "docker" (default), "podman", or "both".
func (p *Provisioner) SetupLocal(runtime string) error {
	defer p.Close()

	switch runtime {
	case "", "docker", "podman", "both", "all":
		p.containerRuntime = runtime
	default:
		return fmt.Errorf("invalid runtime %q: use docker, podman, or both", runtime)
	}

	fmt.Println("Starting Local Setup (Phase 1)...")

	scriptPath := "/tmp/driversInstallation.sh"
	fmt.Println("Staging setup script to /tmp...")
	if err := os.WriteFile(scriptPath, driversInstallScript, 0755); err != nil {
		return fmt.Errorf("failed to stage script: %v", err)
	}

	fmt.Println("Executing Phase 1 (Drivers & Environment)...")
	if err := p.runLocalCommand(scriptPath, "--phase1"); err != nil {
		return fmt.Errorf("phase 1 failed: %v", err)
	}

	fmt.Println("\nPhase 1 complete. After reboot, run: ai-studio-cli setup nvbandwidth")
	return nil
}

func (p *Provisioner) SetupLocalPhase2() error {
	defer p.Close()
	fmt.Println("Starting Local Setup (Phase 2)...")

	scriptPath := "/tmp/driversInstallation.sh"
	fmt.Println("Staging setup script to /tmp...")
	if err := os.WriteFile(scriptPath, driversInstallScript, 0755); err != nil {
		fmt.Printf("Warning: could not stage script: %v\n", err)
	}

	fmt.Println("Validating NVIDIA Drivers...")
	nvsmi := exec.Command("nvidia-smi")
	nvsmi.Stdout = os.Stdout
	nvsmi.Stderr = os.Stderr
	if err := nvsmi.Run(); err != nil {
		fmt.Printf("Warning: nvidia-smi check failed — drivers may not be ready: %v\n", err)
	}

	localNVBPath := "./nvbandwidth"
	if _, err := os.Stat(localNVBPath); os.IsNotExist(err) {
		fmt.Printf("Cloning nvbandwidth from %s...\n", nvbandwidthRepo)
		cloneCmd := exec.Command("git", "clone", nvbandwidthRepo, localNVBPath)
		cloneCmd.Stdout = os.Stdout
		cloneCmd.Stderr = os.Stderr
		if err := cloneCmd.Run(); err != nil {
			return fmt.Errorf("failed to clone nvbandwidth: %v\n  Make sure 'git' is installed and the node has internet access", err)
		}
		fmt.Println("nvbandwidth cloned successfully.")
	} else {
		fmt.Println("nvbandwidth source already present, skipping clone.")
	}

	fmt.Println("Building nvbandwidth...")
	if err := p.runLocalCommand(scriptPath, "--phase2"); err != nil {
		return fmt.Errorf("phase 2 failed: %v", err)
	}

	fmt.Println("Moving nvbandwidth binary to ai-studio-cli directory...")
	exePath, err := os.Executable()
	if err != nil {
		fmt.Println("Warning: could not determine ai-studio-cli directory; binary remains in ./nvbandwidth/nvbandwidth")
		fmt.Println("\nLocal Setup Successful!")
		fmt.Println("You can now run benchmarks using: ai-studio-cli nvbandwidth")
		return nil
	}

	targetBin := filepath.Join(filepath.Dir(exePath), "nvbandwidth")
	uid := os.Getuid()
	gid := os.Getgid()

	moveSteps := [][]string{
		{"cp", "./nvbandwidth/nvbandwidth", "/tmp/nvb-tmp"},
		{"rm", "-rf", "./nvbandwidth"},
		{"mv", "/tmp/nvb-tmp", targetBin},
		{"chmod", "+x", targetBin},
		{"chown", fmt.Sprintf("%d:%d", uid, gid), targetBin},
	}
	for _, step := range moveSteps {
		if err := p.runSudoCmd(step...); err != nil {
			fmt.Printf("Warning: move step %q failed: %v\n", step[0], err)
		}
	}

	fmt.Println("\nLocal Setup Successful!")
	fmt.Println("You can now run benchmarks using: ai-studio-cli nvbandwidth")
	return nil
}

func (p *Provisioner) SetupVLLMDeps() error {
	defer p.Close()
	fmt.Println("Starting vLLM dependency setup...")

	scriptPath := "/tmp/installVllmDeps.sh"
	fmt.Println("Staging vLLM deps script to /tmp...")

	script := bytes.ReplaceAll(installVllmDepsScript, []byte("\r\n"), []byte("\n"))
	if err := os.WriteFile(scriptPath, script, 0755); err != nil {
		return fmt.Errorf("failed to stage script: %v", err)
	}

	fmt.Println("Executing vLLM dependency installation...")
	if err := p.runLocalCommand(scriptPath, ""); err != nil {
		return fmt.Errorf("vLLM dependency setup failed: %v", err)
	}

	fmt.Println("\nvLLM dependency setup complete!")
	fmt.Println("You can now start vLLM using: ai-studio-cli vllm up")
	return nil
}

func (p *Provisioner) runSudoCmd(args ...string) error {
	cmdArgs := append([]string{"-S"}, args...)
	cmd := exec.Command("sudo", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("opening stdin pipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	fmt.Fprintf(stdin, "%s\n", p.sudoPassword)
	stdin.Close()
	return cmd.Wait()
}

func (p *Provisioner) runLocalCommand(script, phase string) error {
	exports := "export DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=a NEEDRESTART_SUSPEND=1"
	if p.containerRuntime != "" {
		exports += fmt.Sprintf(" CONTAINER_RUNTIME=%s", p.containerRuntime)
	}
	shellArgs := []string{exports}
	if phase != "" {
		shellArgs = append(shellArgs, fmt.Sprintf("&& bash -- %s %s", script, phase))
	} else {
		shellArgs = append(shellArgs, fmt.Sprintf("&& bash -- %s", script))
	}

	return p.runSudoCmd("sh", "-c", strings.Join(shellArgs, " "))
}