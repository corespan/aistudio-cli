package cmd

import (
	"github.com/corespan/ai-studio-cli/internal/nvbandwidth"
	"github.com/spf13/cobra"
)

// nvlinkRunner shares flags with the top-level nvbwRunner but is a distinct
// instance so the NVLink subcommand can override defaults independently.
var nvlinkRunner = nvbandwidth.NewRunner()

// nvlinkTestcases are the nvbandwidth testcases that exercise NVLink
// peer-to-peer transfers.  These are the most relevant benchmarks for
// multi-GPU inference systems such as the CoreSpan 5090.
var nvlinkTestcases = []string{
	"device_to_device_memcpy_read_sm",
	"device_to_device_memcpy_write_sm",
	"device_to_device_bidirectional_memcpy_read_sm",
	"device_to_device_bidirectional_memcpy_write_sm",
	"all_to_host_memcpy_sm",
	"host_to_all_memcpy_sm",
}

var nvlinkCmd = &cobra.Command{
	Use:   "nvlink",
	Short: "Run NVLink peer-to-peer bandwidth benchmarks",
	Long: `Runs the nvbandwidth testcases that exercise GPU-to-GPU (NVLink)
bandwidth.  Useful for validating multi-GPU interconnect health on systems
such as the CoreSpan 5090 Inference System.

Testcases run:
  - device_to_device_memcpy_read_sm
  - device_to_device_memcpy_write_sm
  - device_to_device_bidirectional_memcpy_read_sm
  - device_to_device_bidirectional_memcpy_write_sm
  - all_to_host_memcpy_sm
  - host_to_all_memcpy_sm`,
	RunE: func(cmd *cobra.Command, args []string) error {
		for _, tc := range nvlinkTestcases {
			if err := nvlinkRunner.Run([]string{"--testcase", tc}); err != nil {
				return err
			}
		}
		return nil
	},
}

func init() {
	nvlinkCmd.Flags().IntVarP(&nvlinkRunner.BufferSize, "buffer-size", "b", 512, "Memcpy buffer size in MiB")
	nvlinkCmd.Flags().IntVarP(&nvlinkRunner.TestSamples, "samples", "i", 3, "Number of benchmark iterations")
	nvlinkCmd.Flags().BoolVarP(&nvlinkRunner.Verbose, "verbose", "v", false, "Enable verbose output")
	nvlinkCmd.Flags().BoolVarP(&nvlinkRunner.JSON, "json", "j", false, "Output results as JSON")
	nvlinkCmd.Flags().BoolVarP(&nvlinkRunner.SkipVerify, "skip-verify", "s", false, "Skip data verification after copy")
	nvlinkCmd.Flags().BoolVarP(&nvlinkRunner.UseMean, "use-mean", "m", false, "Use arithmetic mean instead of median")

	nvbandwidthCmd.AddCommand(nvlinkCmd)
}
