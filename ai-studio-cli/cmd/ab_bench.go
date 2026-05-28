package cmd

import (
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
	abEndpoint    string
	abAPIPath     string
	abModel       string
	abPrompt      string
	abMaxTokens   int
	abConcurrency int
	abRequests    int
	abOutputFile  string
	abExtraArgs   []string
)

var abBenchCmd = &cobra.Command{
	Use:   "ab-bench",
	Short: "Run Apache Bench (ab) against the vLLM completions endpoint",
	Long: `Sends HTTP POST requests to the vLLM /v1/completions endpoint using
Apache Bench (ab). The JSON request body is constructed from the supplied flags
and written to a temporary file that ab reads via its -p option.

Requirements:
  - ab must be installed (apt install apache2-utils / brew install httpd)
  - The vLLM server must already be running`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runABBench()
	},
}

func init() {
	abBenchCmd.Flags().StringVar(&abEndpoint, "endpoint", "http://localhost:8010", "vLLM base URL")
	abBenchCmd.Flags().StringVar(&abAPIPath, "api-path", "/v1/completions", "API path to POST to")
	abBenchCmd.Flags().StringVar(&abModel, "model", "", "Model name (empty = auto-detect from /v1/models)")
	abBenchCmd.Flags().StringVar(&abPrompt, "prompt", "Hello, world!", "Prompt to send in every request")
	abBenchCmd.Flags().IntVar(&abMaxTokens, "max-tokens", 128, "max_tokens for each completion request")
	abBenchCmd.Flags().IntVar(&abConcurrency, "concurrency", 8, "Number of concurrent ab workers (-c)")
	abBenchCmd.Flags().IntVar(&abRequests, "requests", 100, "Total number of requests to send (-n)")
	abBenchCmd.Flags().StringVar(&abOutputFile, "output", "", "Write ab gnuplot output to this file (ab -g flag); empty = skip")
	abBenchCmd.Flags().StringArrayVar(&abExtraArgs, "ab-arg", nil, "Additional raw argument for ab; can be repeated")

	rootCmd.AddCommand(abBenchCmd)
}

func runABBench() error {
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
		if err := os.MkdirAll(filepath.Dir(abOutputFile), 0755); err != nil && filepath.Dir(abOutputFile) != "." {
			return fmt.Errorf("creating output directory: %w", err)
		}
		abArgs = append(abArgs, "-g", abOutputFile)
	}
	abArgs = append(abArgs, abExtraArgs...)
	abArgs = append(abArgs, fullURL)

	fmt.Printf("Request body: %s\n", string(bodyJSON))
	fmt.Printf("Launching: ab %s\n\n", shellJoin(abArgs))

	cmd := exec.Command("ab", abArgs...)
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