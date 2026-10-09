package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/corespan/ai-studio-cli/internal/transferbench"
	"github.com/spf13/cobra"
)

var tbRunner = transferbench.NewRunner()

var transferbenchCmd = &cobra.Command{
	Use:     "transferbench",
	Aliases: []string{"tb"},
	Short:   "GPU bandwidth benchmarking via AMD TransferBench (ROCm)",
	Long: `Run AMD TransferBench to measure copy bandwidth between host and GPU memory
on ROCm systems — the AMD counterpart to 'ai-studio-cli nvbandwidth'.
Requires the 'TransferBench' binary beside ai-studio-cli, in PATH, or in
$ROCM_PATH/bin. Install it with: ai-studio-cli setup transferbench

TransferBench is configured through environment variables rather than flags.
The common ones have flags here; pass any other with --env KEY=VALUE
('ai-studio-cli transferbench run envvars' lists them).`,
	// Flags are parsed by now, so usage errors still print the usage text;
	// runtime failures (TransferBench missing, a test failing) do not.
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cmd.SilenceUsage = true
	},
}

var tbListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the TransferBench presets",
	RunE: func(cmd *cobra.Command, args []string) error {
		return tbRunner.Run("presets", nil, nil)
	},
}

var tbTopologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "Show the GPUs, CPUs and links TransferBench detects",
	RunE: func(cmd *cobra.Command, args []string) error {
		return tbRunner.Run("", nil, nil)
	},
}

var tbRunCmd = &cobra.Command{
	Use:   "run <preset|config-file>",
	Short: "Run a TransferBench preset or config file",
	Example: `  ai-studio-cli transferbench run p2p
  ai-studio-cli transferbench run a2a --bytes 1G --env A2A_MODE=1
  ai-studio-cli transferbench run ./my-transfers.cfg`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return tbRunner.Run(args[0], nil, nil)
	},
}

var tbTestCmd = &cobra.Command{
	Use:   "test <transfer-expression>...",
	Short: "Run one line of concurrent Transfers given on the command line",
	Long: `Runs a single TransferBench test from an inline expression:
  #Transfers #SubExecs (src->executor->dst) ...
Memory: G = GPU, C = pinned host, F = fine-grained GPU. Executor: G = GPU
kernel, D = DMA engine, C = CPU. All Transfers on the line run at the same time.`,
	Example: `  # GPU 0 kernel copies to GPU 1 using 4 CUs
  ai-studio-cli transferbench test "1 4 (G0->G0->G1)"

  # Both directions at once, 1 GiB each
  ai-studio-cli transferbench test --bytes 1G "2 8 (G0->G0->G1) (G1->G1->G0)"`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := "cmdline"
		if tbDryRun {
			target = "dryrun"
		}
		return tbRunner.Run(target, args, nil)
	},
}

var tbDryRun bool

// ── Fabric shortcuts ─────────────────────────────────────────────────────────
//
// p2p, a2a and bisection are the three tests that matter for validating a
// multi-GPU PCIe fabric: the pairwise matrix finds bad links, all-to-all and
// bisection find oversubscribed ones.

var (
	tbP2PMode     string
	tbP2PDMA      bool
	tbP2PRead     bool
	tbA2ADMA      bool
	tbA2ASE       int
	tbBisectSE    int
	tbBisectDMA   bool
	tbBisectBidir bool
	tbBisectPrint bool
	tbFabricDMA   bool
)

var tbP2PCmd = &cobra.Command{
	Use:   "p2p",
	Short: "GPU-to-GPU bandwidth matrix (TransferBench p2p preset)",
	Long: `Measures every GPU pair, one pair at a time, and prints SRC x DST matrices
in GB/s. A weak row or column points at one bad link; a block of weak cells
points at a shared, oversubscribed link or ACS left on.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		env, err := p2pEnv()
		if err != nil {
			return err
		}
		return tbRunner.Run("p2p", nil, env)
	},
}

var tbA2ACmd = &cobra.Command{
	Use:   "a2a",
	Short: "All-to-all: every GPU sends to every other at once (a2a preset)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return tbRunner.Run("a2a", nil, a2aEnv())
	},
}

var tbBisectionCmd = &cobra.Command{
	Use:   "bisection",
	Short: "Lower half of the GPUs sends to the upper half at once",
	Long: `Runs one GPU pair alone, then GPUs 0..N/2-1 sending to N/2..N-1 at the same
time. Pairwise tests never fill a switch uplink, so they cannot show whether a
fabric is non-blocking; this one can. On a non-blocking fabric the per-Transfer
bandwidth in the second test stays close to the first.

GPU numbering follows TransferBench's (see 'transferbench topology'), which
need not match the physical layout. Check which GPUs sit behind which switch
before reading the halves as "PRU 1" and "PRU 2".`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBisection(tbRunner, cmd.Flags().Changed("sub-execs"))
	},
}

var tbFabricCmd = &cobra.Command{
	Use:   "fabric",
	Short: "Run p2p, a2a and bisection in sequence",
	Long: `Runs the fabric validation set in order, stopping at the first failure:
  1. p2p        — pairwise matrix, finds bad links
  2. a2a        — all-to-all at once, finds contention
  3. bisection  — half-to-half at once, proves (or disproves) non-blocking
The AMD counterpart to 'ai-studio-cli nvbandwidth nvlink'.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		tbP2PDMA, tbA2ADMA, tbBisectDMA = tbFabricDMA, tbFabricDMA, tbFabricDMA
		env, err := p2pEnv()
		if err != nil {
			return err
		}
		steps := []struct {
			name string
			run  func() error
		}{
			{"p2p", func() error { return tbRunner.Run("p2p", nil, env) }},
			{"a2a", func() error { return tbRunner.Run("a2a", nil, a2aEnv()) }},
			{"bisection", func() error { return runBisection(tbRunner, false) }},
		}
		for i, s := range steps {
			fmt.Printf("\n=== [%d/%d] %s ===\n", i+1, len(steps), s.name)
			if err := s.run(); err != nil {
				return fmt.Errorf("%s: %w", s.name, err)
			}
		}
		return nil
	},
}

func p2pEnv() (map[string]string, error) {
	env := map[string]string{}
	switch tbP2PMode {
	case "both":
		env["P2P_MODE"] = "0"
	case "uni":
		env["P2P_MODE"] = "1"
	case "bi":
		env["P2P_MODE"] = "2"
	default:
		return nil, fmt.Errorf("invalid --mode %q: use uni, bi, or both", tbP2PMode)
	}
	if tbP2PDMA {
		env["USE_GPU_DMA"] = "1"
	}
	if tbP2PRead {
		env["USE_REMOTE_READ"] = "1"
	}
	return env, nil
}

func a2aEnv() map[string]string {
	env := map[string]string{"NUM_SUB_EXEC": strconv.Itoa(tbA2ASE)}
	if tbA2ADMA {
		env["USE_DMA_EXEC"] = "1"
	}
	return env
}

// runBisection generates the bisection config and runs it. seSet reports
// whether --sub-execs was given; with --dma the default drops to one queue.
func runBisection(r *transferbench.Runner, seSet bool) error {
	n := r.NumGPUs
	if n == 0 {
		detected, err := transferbench.DetectGPUs("/")
		if err != nil {
			return fmt.Errorf("%w\n  Pass --num-gpus to set the GPU count explicitly", err)
		}
		n = detected
	}
	se := tbBisectSE
	if tbBisectDMA && !seSet {
		se = 1 // one DMA queue per Transfer unless asked otherwise
	}
	cfg, err := transferbench.BisectionConfig(transferbench.BisectionOptions{
		NumGPUs: n, SubExecs: se, DMA: tbBisectDMA, Bidir: tbBisectBidir,
	})
	if err != nil {
		return err
	}
	if tbBisectPrint {
		fmt.Print(cfg)
		return nil
	}

	f, err := os.CreateTemp("", "transferbench-bisection-*.cfg")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(cfg); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return r.Run(f.Name(), nil, nil)
}

func init() {
	// Shared flags. All subcommands drive the same runner, so each binds the
	// same fields; only the flags given on the command line take effect.
	for _, c := range []*cobra.Command{tbRunCmd, tbTestCmd, tbP2PCmd, tbA2ACmd, tbBisectionCmd, tbFabricCmd} {
		c.Flags().StringVarP(&tbRunner.Bytes, "bytes", "b", "", "Bytes per Transfer, K/M/G suffixes allowed (default 256M; 0 sweeps sizes)")
		c.Flags().IntVarP(&tbRunner.Iterations, "iterations", "i", 0, "Timed iterations, NUM_ITERATIONS (default 10; negative = seconds)")
		c.Flags().StringVarP(&tbRunner.Warmups, "warmups", "w", "", "Warmup iterations, NUM_WARMUPS (TransferBench default 3)")
		c.Flags().IntVarP(&tbRunner.NumGPUs, "num-gpus", "n", 0, "Use the first N GPUs, NUM_GPU_DEVICES (default all)")
		c.Flags().BoolVar(&tbRunner.CSV, "csv", false, "Output results as CSV, OUTPUT_TO_CSV=1 (TransferBench has no JSON output)")
		c.Flags().StringArrayVarP(&tbRunner.Env, "env", "e", nil, "Extra TransferBench environment variable KEY=VALUE (repeatable)")
	}
	tbTestCmd.Flags().BoolVar(&tbDryRun, "dry-run", false, "Print the parsed Transfers without running them")

	const dmaHelp = "Copy with DMA engines instead of GPU kernels"
	for _, c := range []*cobra.Command{tbP2PCmd, tbFabricCmd} {
		c.Flags().StringVar(&tbP2PMode, "mode", "both", "p2p directions: uni, bi, or both")
		c.Flags().BoolVar(&tbP2PRead, "remote-read", false, "p2p: the destination GPU reads instead of the source writing")
	}
	tbP2PCmd.Flags().BoolVar(&tbP2PDMA, "dma", false, dmaHelp)

	tbA2ACmd.Flags().BoolVar(&tbA2ADMA, "dma", false, dmaHelp)
	tbA2ACmd.Flags().IntVar(&tbA2ASE, "sub-execs", 8, "CUs per Transfer, NUM_SUB_EXEC")

	tbBisectionCmd.Flags().BoolVar(&tbBisectDMA, "dma", false, dmaHelp)
	tbBisectionCmd.Flags().IntVar(&tbBisectSE, "sub-execs", 8, "CUs per Transfer (DMA queues with --dma, where the default is 1)")
	tbBisectionCmd.Flags().BoolVar(&tbBisectPrint, "print-config", false, "Print the generated TransferBench config and exit")
	for _, c := range []*cobra.Command{tbBisectionCmd, tbFabricCmd} {
		c.Flags().BoolVar(&tbBisectBidir, "bidirectional", false, "bisection: upper half also sends to lower half at the same time")
	}
	tbFabricCmd.Flags().BoolVar(&tbFabricDMA, "dma", false, dmaHelp+" (all three tests)")

	transferbenchCmd.AddCommand(tbListCmd, tbTopologyCmd, tbRunCmd, tbTestCmd,
		tbP2PCmd, tbA2ACmd, tbBisectionCmd, tbFabricCmd)
	rootCmd.AddCommand(transferbenchCmd)
}
