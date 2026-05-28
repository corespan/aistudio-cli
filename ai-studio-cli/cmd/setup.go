package cmd

import (
	"fmt"
	"os"

	"github.com/corespan/ai-studio-cli/internal/config"
	"github.com/corespan/ai-studio-cli/internal/provision"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Setup and provisioning operations",
}

var setupDependenciesCmd = &cobra.Command{
	Use:   "dependencies",
	Short: "Install system dependencies (Docker, CUDA, NVIDIA Drivers)",
	Long: `Automates the first phase of setup using local assets:
- Runs the local driversInstallation.sh script (Phase 1)
- The script must be present in the ./assets/ directory
- The nvbandwidth source will be cloned automatically from GitHub in Phase 2`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := prepareProvisioner()
		if err != nil {
			return err
		}
		return p.SetupLocal()
	},
}

var setupNVBandwidthCmd = &cobra.Command{
	Use:   "nvbandwidth",
	Short: "Build and install the nvbandwidth tool",
	Long: `Automates the second phase of setup (after reboot):
- Verifies NVIDIA drivers are working
- Clones the nvbandwidth source from GitHub (https://github.com/NVIDIA/nvbandwidth)
- Compiles the nvbandwidth tool
- Moves the binary to the ai-studio-cli directory`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := prepareProvisioner()
		if err != nil {
			return err
		}
		return p.SetupLocalPhase2()
	},
}

var setupVLLMCmd = &cobra.Command{
	Use:   "vllm",
	Short: "Install dependencies for vLLM (Docker, NVIDIA Container Toolkit)",
	Long: `Automates the installation of vLLM requirements:
- Installs Docker Engine if Docker is not already installed
- Installs NVIDIA Container Toolkit if a GPU is detected`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := prepareProvisioner()
		if err != nil {
			return err
		}
		return p.SetupVLLMDeps()
	},
}

func prepareProvisioner() (*provision.Provisioner, error) {
	var passBytes []byte

	if cfgPass := config.GetSetupSudoPassword(); cfgPass != "" {
		passBytes = []byte(cfgPass)
	} else {
		fmt.Print("Enter local sudo password: ")
		var err error
		passBytes, err = term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return nil, fmt.Errorf("failed to read sudo password: %v", err)
		}
		fmt.Println()
	}

	p := provision.NewProvisioner(passBytes)

	for i := range passBytes {
		passBytes[i] = 0
	}

	return p, nil
}

func init() {
	setupCmd.AddCommand(setupDependenciesCmd)
	setupCmd.AddCommand(setupNVBandwidthCmd)
	setupCmd.AddCommand(setupVLLMCmd)
	rootCmd.AddCommand(setupCmd)
}