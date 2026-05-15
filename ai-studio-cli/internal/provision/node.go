package provision

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// assetsDir is the directory (relative to the running binary) where
// driversInstallation.sh must be placed before running any setup command.
// The nvbandwidth source is cloned automatically from GitHub.
const assetsDir = "assets"

// nvbandwidthRepo is the official NVIDIA nvbandwidth GitHub repository.
const nvbandwidthRepo = "https://github.com/NVIDIA/nvbandwidth.git"

type Provisioner struct {
	SudoPassword string
}

func NewProvisioner(sudoPass string) *Provisioner {
	return &Provisioner{SudoPassword: sudoPass}
}

// localAssetsPath returns the absolute path to the assets directory,
// looking first next to the running binary, then falling back to the
// current working directory.
func localAssetsPath() string {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), assetsDir)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	// fallback: cwd/assets
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, assetsDir)
}

// SetupLocal runs Phase 1 of the provisioning using the local driversInstallation.sh.
// It expects:
//
//	<assetsDir>/driversInstallation.sh
//
// The nvbandwidth source is NOT required here — it is cloned automatically in Phase 2.
func (p *Provisioner) SetupLocal() error {
	fmt.Println("Starting Local Setup (Phase 1)...")

	assets := localAssetsPath()
	scriptSrc := filepath.Join(assets, "driversInstallation.sh")

	// Validate that the local script exists before proceeding.
	if _, err := os.Stat(scriptSrc); os.IsNotExist(err) {
		return fmt.Errorf(
			"driversInstallation.sh not found in %s\n"+
				"  Place the script there before running this command.", assets)
	}

	// Copy the script to /tmp so it can be executed with sudo.
	scriptPath := "/tmp/driversInstallation.sh"
	fmt.Printf("Copying setup script from %s...\n", scriptSrc)
	if err := copyFile(scriptSrc, scriptPath); err != nil {
		return fmt.Errorf("failed to stage script: %v", err)
	}

	fmt.Println("Executing Phase 1 (Drivers & Environment)...")
	if err := p.runLocalCommand(scriptPath, "--phase1"); err != nil {
		return fmt.Errorf("phase 1 failed: %v", err)
	}

	// The script handles the reboot; this is a fallback message.
	fmt.Println("\nPhase 1 complete. After reboot, run: ai-studio-cli setup nvbandwidth")
	return nil
}

// SetupLocalPhase2 runs Phase 2: clones the nvbandwidth repo from GitHub,
// builds it using the provisioning script, and installs the binary.
func (p *Provisioner) SetupLocalPhase2() error {
	fmt.Println("Starting Local Setup (Phase 2)...")

	assets := localAssetsPath()
	scriptSrc := filepath.Join(assets, "driversInstallation.sh")

	// Stage the script (re-copy to avoid stale /tmp version).
	scriptPath := "/tmp/driversInstallation.sh"
	fmt.Printf("Staging setup script from %s...\n", scriptSrc)
	if err := copyFile(scriptSrc, scriptPath); err != nil {
		fmt.Printf("Warning: could not stage script: %v\n", err)
	}

	// Validate NVIDIA drivers.
	fmt.Println("Validating NVIDIA Drivers...")
	nvsmi := exec.Command("nvidia-smi")
	nvsmi.Stdout = os.Stdout
	nvsmi.Stderr = os.Stderr
	if err := nvsmi.Run(); err != nil {
		fmt.Printf("Warning: nvidia-smi check failed — drivers may not be ready: %v\n", err)
	}

	// Clone nvbandwidth from GitHub if not already present.
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

	// Move the compiled binary next to the ai-studio-cli binary.
	fmt.Println("Moving nvbandwidth binary to ai-studio-cli directory...")
	exePath, err := os.Executable()
	if err == nil {
		targetBin := filepath.Join(filepath.Dir(exePath), "nvbandwidth")
		moveCmd := fmt.Sprintf(
			"cp ./nvbandwidth/nvbandwidth /tmp/nvb-tmp && rm -rf ./nvbandwidth && mv /tmp/nvb-tmp %s && chmod +x %s && chown $(id -u):$(id -g) %s",
			targetBin, targetBin, targetBin,
		)
		cmd := exec.Command("sudo", "-S", "sh", "-c", moveCmd)
		if stdin, err := cmd.StdinPipe(); err == nil {
			if cmd.Start() == nil {
				fmt.Fprintf(stdin, "%s\n", p.SudoPassword)
				stdin.Close()
				if err := cmd.Wait(); err != nil {
					fmt.Printf("Warning: Could not move binary: %v\n", err)
				}
			}
		}
	} else {
		fmt.Println("Warning: Could not determine ai-studio-cli directory. Binary remains in ./nvbandwidth/nvbandwidth")
	}

	fmt.Println("\nLocal Setup Successful!")
	fmt.Println("You can now run benchmarks using: ai-studio-cli nvbandwidth")
	return nil
}

// runLocalCommand runs the provisioning script under sudo.
// The sudo password is passed via stdin to avoid ps aux exposure.
func (p *Provisioner) runLocalCommand(script, phase string) error {
	cmdStr := fmt.Sprintf(
		"export DEBIAN_FRONTEND=noninteractive; export NEEDRESTART_MODE=a; export NEEDRESTART_SUSPEND=1; bash %s %s",
		script, phase,
	)
	cmd := exec.Command("sudo", "-S", "sh", "-c", cmdStr)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to open stdin pipe: %v", err)
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	fmt.Fprintf(stdin, "%s\n", p.SudoPassword)
	stdin.Close()

	return cmd.Wait()
}

// copyFile copies a single file src → dst, creating dst if needed.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0755)
}

// copyDir recursively copies a directory tree src → dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
