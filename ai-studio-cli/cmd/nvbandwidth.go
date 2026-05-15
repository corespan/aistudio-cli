package cmd

import (
	"github.com/corespan/ai-studio-cli/internal/nvbandwidth"
	"github.com/spf13/cobra"
)

var nvbwRunner = nvbandwidth.NewRunner()

var nvbandwidthCmd = &cobra.Command{
	Use:   "nvbandwidth",
	Short: "GPU bandwidth benchmarking via nvbandwidth",
	Long: `Run NVIDIA nvbandwidth tests to measure memory bandwidth.
Requires the 'nvbandwidth' binary to be present beside nvbw-cli or in PATH.`,
}

var nvbwListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available nvbandwidth testcases",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nvbwRunner.Run([]string{"--list"})
	},
}

var nvbwRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run all nvbandwidth testcases",
	RunE: func(cmd *cobra.Command, args []string) error {
		return nvbwRunner.Run([]string{})
	},
}

var nvbwTestCmd = &cobra.Command{
	Use:   "test <testcase-name>",
	Short: "Run a specific nvbandwidth testcase by name",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return nvbwRunner.Run([]string{"--testcase", args[0]})
	},
}

func init() {
	for _, c := range []*cobra.Command{nvbwRunCmd, nvbwTestCmd} {
		c.Flags().IntVarP(&nvbwRunner.BufferSize, "buffer-size", "b", 512, "Memcpy buffer size in MiB")
		c.Flags().IntVarP(&nvbwRunner.TestSamples, "samples", "i", 3, "Number of benchmark iterations")
		c.Flags().IntVarP(&nvbwRunner.Duration, "duration", "d", 0, "Duration of the test (if supported)")
		c.Flags().StringVar(&nvbwRunner.Src, "src", "", "Source device/node")
		c.Flags().StringVar(&nvbwRunner.Dest, "dest", "", "Destination device/node")
		c.Flags().BoolVarP(&nvbwRunner.Verbose, "verbose", "v", false, "Enable verbose output")
		c.Flags().BoolVarP(&nvbwRunner.JSON, "json", "j", false, "Output results as JSON")
		c.Flags().BoolVarP(&nvbwRunner.SkipVerify, "skip-verify", "s", false, "Skip data verification after copy")
		c.Flags().BoolVarP(&nvbwRunner.UseMean, "use-mean", "m", false, "Use arithmetic mean instead of median")
	}

	nvbandwidthCmd.AddCommand(nvbwListCmd)
	nvbandwidthCmd.AddCommand(nvbwRunCmd)
	nvbandwidthCmd.AddCommand(nvbwTestCmd)
	rootCmd.AddCommand(nvbandwidthCmd)
}
