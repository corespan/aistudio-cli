package provision

import (
        _ "embed"
        "fmt"
        "os"
        "os/exec"
        "path/filepath"
)

//go:embed assets/driversInstallation.sh
var driversInstallScript []byte

const nvbandwidthRepo = "https://github.com/NVIDIA/nvbandwidth.git"

type Provisioner struct {
        SudoPassword string
}

func NewProvisioner(sudoPass string) *Provisioner {
        return &Provisioner{SudoPassword: sudoPass}
}

func (p *Provisioner) SetupLocal() error {
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