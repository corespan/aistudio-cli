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

func prepareProvisioner() (*provision.Provisioner, error) {
	var sudoPassword string
	if cfgPass := config.GetSetupSudoPassword(); cfgPass != "" {
		sudoPassword = cfgPass
	} else {
		fmt.Print("Enter local sudo password: ")
		passBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return nil, fmt.Errorf("failed to read sudo password: %v", err)
		}
		fmt.Println()
		sudoPassword = string(passBytes)
	}

	return provision.NewProvisioner(sudoPassword), nil
}

func init() {
	setupCmd.AddCommand(setupDependenciesCmd)
	setupCmd.AddCommand(setupNVBandwidthCmd)
	rootCmd.AddCommand(setupCmd)
}
