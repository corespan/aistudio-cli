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

var setupRuntime string

var setupDependenciesCmd = &cobra.Command{
	Use:   "dependencies",
	Short: "Install system dependencies (CUDA, NVIDIA Drivers, container runtime)",
	Long: `Automates the first phase of setup using local assets:
- Runs the local driversInstallation.sh script (Phase 1)
- Configures GPU access for the chosen container runtime (--runtime)
- The nvbandwidth source will be cloned automatically from GitHub in Phase 2`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := prepareProvisioner()
		if err != nil {
			return err
		}
		return p.SetupLocal(setupRuntime)
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

var setupTransferBenchRef string

var setupTransferBenchCmd = &cobra.Command{
	Use:   "transferbench",
	Short: "Build and install AMD TransferBench (ROCm nodes)",
	Long: `Builds AMD TransferBench, the ROCm counterpart to nvbandwidth:
- Checks the amdgpu driver (/dev/kfd) and ROCm (default /opt/rocm) are present
- Installs build dependencies (git, make, libnuma-dev)
- Clones TransferBench (https://github.com/ROCm/TransferBench) at --ref
- Compiles it for the GPUs in this node
- Moves the binary to the ai-studio-cli directory

The amdgpu driver and ROCm must already be installed: 'setup dependencies'
provisions NVIDIA nodes only.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := prepareProvisioner()
		if err != nil {
			return err
		}
		return p.SetupTransferBench(setupTransferBenchRef)
	},
}

var setupVLLMRuntime string

var setupVLLMCmd = &cobra.Command{
	Use:   "vllm",
	Short: "Install dependencies for vLLM (Docker/Podman, NVIDIA Container Toolkit)",
	Long: `Automates the installation of vLLM requirements:
- Installs the chosen container runtime (--runtime: docker, podman, or both)
- Installs NVIDIA Container Toolkit if a GPU is detected
- Configures GPU access (Docker NVIDIA runtime, or a Podman CDI spec)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := prepareProvisioner()
		if err != nil {
			return err
		}
		return p.SetupVLLMDeps(setupVLLMRuntime)
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
	setupDependenciesCmd.Flags().StringVar(&setupRuntime, "runtime", "docker",
		"GPU container runtime to configure: docker, podman, or both")
	setupVLLMCmd.Flags().StringVar(&setupVLLMRuntime, "runtime", "docker",
		"Container runtime to install for vLLM: docker, podman, or both")
	setupTransferBenchCmd.Flags().StringVar(&setupTransferBenchRef, "ref", provision.DefaultTransferBenchRef,
		"TransferBench git tag or branch to build")

	setupCmd.AddCommand(setupDependenciesCmd)
	setupCmd.AddCommand(setupNVBandwidthCmd)
	setupCmd.AddCommand(setupTransferBenchCmd)
	setupCmd.AddCommand(setupVLLMCmd)
	rootCmd.AddCommand(setupCmd)
}