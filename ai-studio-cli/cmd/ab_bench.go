package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var (
	abEndpoint      string
	abAPIPath       string
	abModel         string
	abPrompt        string
	abMaxTokens     int
	abMinTokens     int
	abIgnoreEOS     bool
	abConcurrency   int
	abRequests      int
	abOutputFile    string
	abExtraArgs     []string
	abComposeFile   string
	abKeepServer    bool
	abServerTimeout int
)

var abBenchCmd = &cobra.Command{
	Use:   "ab-bench",
	Short: "Run Apache Bench (ab) against the vLLM completions endpoint",
	Long: `Sends HTTP POST requests to the vLLM /v1/completions endpoint using
Apache Bench (ab). The JSON request body is constructed from the supplied flags
and written to a temporary file that ab reads via its -p option.

Server lifecycle:
  - If --compose-file is given, ab-bench will start the server before the run
    and (by default) tear it down afterwards.
  - If --compose-file is omitted and the server is unreachable, the bundled
    default docker-compose.yaml is used automatically.
  - If the server is already reachable, compose up is skipped entirely.
  - Pass --keep-server to leave the server running after the benchmark.

Requirements:
  - ab must be installed (apt install apache2-utils / brew install httpd)
  - The vLLM server must already be running`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runABBench(cmd.Context())
	},
}

func init() {
	abBenchCmd.Flags().StringVar(&abEndpoint, "endpoint", "http://localhost:8010", "vLLM base URL")
	abBenchCmd.Flags().StringVar(&abAPIPath, "api-path", "/v1/completions", "API path to POST to")
	abBenchCmd.Flags().StringVar(&abModel, "model", "", "Model name (empty = auto-detect from /v1/models)")
	abBenchCmd.Flags().StringVar(&abPrompt, "prompt", "Hello, world!", "Prompt to send in every request")
	abBenchCmd.Flags().IntVar(&abMaxTokens, "max-tokens", 256, "max_tokens for each completion request")
	abBenchCmd.Flags().IntVar(&abMinTokens, "min-tokens", 0, "min_tokens for force-decoding long responses (0 to disable)")
	abBenchCmd.Flags().BoolVar(&abIgnoreEOS, "ignore-eos", false, "Ask vLLM to ignore EOS during benchmarking")
	abBenchCmd.Flags().IntVar(&abConcurrency, "concurrency", 128, "Number of concurrent ab workers (-c); match the server's --max-num-seqs")
	abBenchCmd.Flags().IntVar(&abRequests, "requests", 1000, "Total number of requests to send (-n)")
	abBenchCmd.Flags().StringVar(&abOutputFile, "output", "", "Write ab gnuplot output to this file (ab -g flag); empty = skip")
	abBenchCmd.Flags().StringArrayVar(&abExtraArgs, "ab-arg", nil, "Additional raw argument for ab; can be repeated (advanced: do not interpolate untrusted input)")
	abBenchCmd.Flags().StringVar(&abComposeFile, "compose-file", "", "Path to a custom docker-compose.yml; uses bundled default if server is unreachable and this is unset")
	abBenchCmd.Flags().BoolVar(&abKeepServer, "keep-server", false, "Leave the vLLM server running after the benchmark completes")
	abBenchCmd.Flags().IntVar(&abServerTimeout, "server-timeout", 900, "Maximum seconds to wait for the vLLM server to become ready (default allows for large model load times)")

	rootCmd.AddCommand(abBenchCmd)
}

func runABBench(ctx context.Context) error {
	_, cleanup, err := serverLifecycle(abComposeFile, abEndpoint, abServerTimeout, abKeepServer)
	if err != nil {
		return err
	}
	defer cleanup()

	return runABBenchCore(ctx)
}

func runABBenchCore(ctx context.Context) error {
	// --- resolve model ---
	modelName := abModel
	if modelName == "" {
		fmt.Printf("No model specified; querying %s/v1/models...\n", abEndpoint)
		models, err := getVLLMModels(abEndpoint)
		if err != nil {
			return fmt.Errorf("failed to auto-detect model: %w\n  Hint: is the vLLM server running? Or pass --model explicitly", err)
		}
		if len(models) == 0 {
			return fmt.Errorf("no models returned by vLLM at %s; pass --model explicitly", abEndpoint)
		}
		modelName = models[0]
		fmt.Printf("Using model: %s\n", modelName)
	}

	// --- build JSON body ---
	body := map[string]any{
		"model":      modelName,
		"prompt":     abPrompt,
		"max_tokens": abMaxTokens,
		"ignore_eos": abIgnoreEOS,
	}

	if abMinTokens > 0 {
		body["min_tokens"] = abMinTokens
	}
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshalling request body: %w", err)
	}

	// --- write body to temp file (ab -p requires a file) ---
	tmpFile, err := os.CreateTemp("", "ab-bench-body-*.json")
	if err != nil {
		return fmt.Errorf("creating temp body file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(bodyJSON); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing body file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing body file: %w", err)
	}

	// --- build full URL ---
	apiPath := abAPIPath
	if !strings.HasPrefix(apiPath, "/") {
		apiPath = "/" + apiPath
	}
	base := strings.TrimSuffix(abEndpoint, "/")
	fullURL := base + apiPath

	// --- assemble ab arguments ---
	abArgs := []string{
		"-n", strconv.Itoa(abRequests),
		"-c", strconv.Itoa(abConcurrency),
		"-p", tmpFile.Name(),
		"-T", "application/json",
	}
	if abOutputFile != "" {
		if dir := filepath.Dir(abOutputFile); dir != "." {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return fmt.Errorf("creating output directory: %w", err)
			}
		}
		abArgs = append(abArgs, "-g", abOutputFile)
	}
	abArgs = append(abArgs, abExtraArgs...)
	abArgs = append(abArgs, fullURL)

	fmt.Printf("Request body: %s\n", string(bodyJSON))
	fmt.Printf("Launching: ab %s\n\n", shellJoin(abArgs))

	cmd := exec.CommandContext(ctx, "ab", abArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ab failed: %w\n  Hint: make sure 'ab' is installed (apt install apache2-utils)", err)
	}

	if abOutputFile != "" {
		fmt.Printf("\nGnuplot data saved to: %s\n", abOutputFile)
	}
	return nil
}
