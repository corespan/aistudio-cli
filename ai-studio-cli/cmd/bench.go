package cmd

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type benchRunner struct {
	endpoint          string
	apiEndpoint       string
	model             string
	concurrency       int
	requests          int
	maxTokens         int
	dataset           string
	datasetPath       string
	warmup            int
	promptStr         string
	inputLen          int
	requestRate       string
	resultDir         string
	resultFilename    string
	percentileMetrics string
	metricPercentiles string
	ignoreEOS         bool
	skipChatTemplate  bool
	trustRemoteCode   bool
	appendResult      bool
	saveDetailed      bool
	metadata          []string
	additionalArgs    []string
	gpuTag            string
	vllmImage         string
}

var bench = &benchRunner{}

var benchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Run vLLM's built-in serving benchmark",
	RunE: func(cmd *cobra.Command, args []string) error {
		return bench.run(cmd)
	},
}

func init() {
	bench.registerFlags(benchCmd)
	rootCmd.AddCommand(benchCmd)
}

func (r *benchRunner) registerFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&r.endpoint, "endpoint", "http://localhost:8010", "vLLM base URL or full endpoint URL")
	cmd.Flags().StringVar(&r.apiEndpoint, "api-endpoint", "/v1/completions", "API path for vLLM benchmark requests")
	cmd.Flags().StringVar(&r.model, "model", "", "Model name to request; if empty, auto-detected from /v1/models")
	cmd.Flags().IntVar(&r.concurrency, "concurrency", 8, "Maximum concurrent benchmark requests")
	cmd.Flags().IntVar(&r.requests, "requests", 50, "Total benchmark prompts to send")
	cmd.Flags().IntVar(&r.maxTokens, "max-tokens", 128, "Target output token length")
	cmd.Flags().StringVar(&r.dataset, "dataset", "random", "Dataset name for vLLM bench: random, sonnet, sharegpt, custom, hf, etc.")
	cmd.Flags().StringVar(&r.datasetPath, "dataset-path", "", "Dataset path for datasets that require one")
	cmd.Flags().IntVar(&r.warmup, "warmup", 2, "Number of warmup requests")
	cmd.Flags().StringVar(&r.promptStr, "prompt", "", "Single prompt string to repeat for every request")
	cmd.Flags().IntVar(&r.inputLen, "input-len", 128, "Target input token length for synthetic datasets")
	cmd.Flags().StringVar(&r.requestRate, "request-rate", "inf", "Request rate passed to vLLM bench serve")
	cmd.Flags().StringVar(&r.resultDir, "result-dir", "bench-results", "Directory for vLLM benchmark result JSON")
	cmd.Flags().StringVar(&r.resultFilename, "result-filename", "benchmark_result.json", "Filename for vLLM benchmark result JSON")
	cmd.Flags().StringVar(&r.percentileMetrics, "percentile-metrics", "ttft,tpot,itl,e2el", "vLLM percentile metrics to report")
	cmd.Flags().StringVar(&r.metricPercentiles, "metric-percentiles", "50,90,95,99", "Percentiles to report for selected metrics")
	cmd.Flags().BoolVar(&r.ignoreEOS, "ignore-eos", true, "Ask vLLM to ignore EOS during benchmarking")
	cmd.Flags().BoolVar(&r.skipChatTemplate, "skip-chat-template", true, "Skip chat template for datasets that support it")
	cmd.Flags().BoolVar(&r.trustRemoteCode, "trust-remote-code", false, "Pass --trust-remote-code to vLLM bench")
	cmd.Flags().BoolVar(&r.appendResult, "append-result", false, "Append to the result JSON instead of replacing it")
	cmd.Flags().BoolVar(&r.saveDetailed, "save-detailed", false, "Ask vLLM bench to save per-request details")
	cmd.Flags().StringArrayVar(&r.metadata, "metadata", nil, "Metadata key=value to save in the result JSON; can be repeated")
	cmd.Flags().StringArrayVar(&r.additionalArgs, "vllm-arg", nil, "Additional raw argument for vllm bench serve; can be repeated")
	cmd.Flags().StringVar(&r.gpuTag, "gpu-tag", "", "GPU tag for structured result path (auto-detected if empty)")
	cmd.Flags().StringVar(&r.vllmImage, "vllm-image", "vllm/vllm-openai:latest", "Docker image to use for benchmarking if vllm is not installed locally")
}

// entry point
func (r *benchRunner) run(cmd *cobra.Command) error {
	apiEndpointChanged := cmd.Flags().Changed("api-endpoint")
	host, port, apiEndpoint, err := splitBenchmarkEndpoint(r.endpoint, r.apiEndpoint, apiEndpointChanged)
	if err != nil {
		return err
	}

	datasetName := r.resolveDatasetName()
	modelName, err := r.resolveModel()
	if err != nil {
		return err
	}

	gpu := r.gpuTag
	if gpu == "" {
		gpu = detectGPUTag()
	}

	timestamp := time.Now().Format("2006-01-02T15-04-05")
	structuredDir := filepath.Join(r.resultDir, sanitizePath(modelName), sanitizePath(gpu), timestamp)

	args := r.buildArgs(host, port, apiEndpoint, modelName, datasetName, structuredDir)

	cleanup, err := r.applyPromptDataset(&args)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := os.MkdirAll(structuredDir, 0755); err != nil {
		return fmt.Errorf("creating result directory: %w", err)
	}

	if err := r.execute(args, structuredDir); err != nil {
		return err
	}

	resultPath := filepath.Join(structuredDir, r.resultFilename)
	fmt.Printf("\nResult saved to: %s\n", resultPath)
	printPerGPUThroughput(resultPath)
	return nil
}

func (r *benchRunner) resolveDatasetName() string {
	if r.promptStr != "" {
		return "custom"
	}
	name := strings.ToLower(strings.TrimSpace(r.dataset))
	if name == "" {
		return "random"
	}
	return name
}

func (r *benchRunner) resolveModel() (string, error) {
	if r.model != "" {
		return r.model, nil
	}

	baseEndpoint := r.endpoint
	if parsed, err := url.Parse(r.endpoint); err == nil {
		baseEndpoint = fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
	}

	fmt.Printf("No model specified; querying %s/v1/models...\n", baseEndpoint)
	models, err := getVLLMModels(baseEndpoint)
	if err != nil || len(models) == 0 {
		fmt.Printf("Warning: failed to auto-detect model from %s/v1/models: %v\n", baseEndpoint, err)
		return "unknown-model", nil
	}
	fmt.Printf("Using model: %s\n", models[0])
	return models[0], nil
}

func (r *benchRunner) buildArgs(host, port, apiEndpoint, modelName, datasetName, structuredDir string) []string {
	args := []string{
		"bench", "serve",
		"--backend", backendForEndpoint(apiEndpoint),
		"--host", host,
		"--port", port,
		"--endpoint", apiEndpoint,
		"--dataset-name", datasetName,
		"--num-prompts", strconv.Itoa(r.requests),
		"--max-concurrency", strconv.Itoa(r.concurrency),
		"--request-rate", r.requestRate,
		"--num-warmups", strconv.Itoa(r.warmup),
		"--random-input-len", strconv.Itoa(r.inputLen),
		"--random-output-len", strconv.Itoa(r.maxTokens),
		"--percentile-metrics", r.percentileMetrics,
		"--metric-percentiles", r.metricPercentiles,
		"--save-result",
		"--result-dir", structuredDir,
		"--result-filename", r.resultFilename,
	}

	if r.model != "" {
		args = append(args, "--model", modelName)
	}
	if r.datasetPath != "" && r.promptStr == "" {
		args = append(args, "--dataset-path", r.datasetPath)
	}
	if r.ignoreEOS {
		args = append(args, "--ignore-eos")
	}
	if r.skipChatTemplate {
		args = append(args, "--skip-chat-template")
	}
	if r.trustRemoteCode {
		args = append(args, "--trust-remote-code")
	}
	if r.appendResult {
		args = append(args, "--append-result")
	}
	if r.saveDetailed {
		args = append(args, "--save-detailed")
	}
	for _, item := range r.metadata {
		args = append(args, "--metadata", item)
	}
	args = append(args, r.additionalArgs...)
	return args
}

func (r *benchRunner) execute(args []string, structuredDir string) error {
	if isVllmAvailable() {
		fmt.Printf("Launching: vllm %s\n", shellJoin(args))
		cmd := exec.Command("vllm", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("vllm bench serve failed: %w", err)
		}
		return nil
	}

	if isDockerAvailable() {
		fmt.Println("vllm not found locally; falling back to Docker.")
		if err := r.runInDocker(args, structuredDir); err != nil {
			return fmt.Errorf("vllm bench serve via Docker failed: %w", err)
		}
		return nil
	}

	return fmt.Errorf("neither 'vllm' nor 'docker' commands found; please install vLLM locally or install Docker")
}

// applyPromptDataset writes the --prompt string to a temporary JSONL file
func (r *benchRunner) applyPromptDataset(args *[]string) (func(), error) {
	if r.promptStr == "" {
		return func() {}, nil
	}

	dir, err := os.MkdirTemp("", "ai-studio-cli-bench-*")
	if err != nil {
		return nil, fmt.Errorf("creating prompt dataset dir: %w", err)
	}

	path := filepath.Join(dir, "prompts.jsonl")
	f, err := os.Create(path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("creating prompt dataset file: %w", err)
	}

	encoder := json.NewEncoder(f)
	for i := 0; i < r.requests; i++ {
		if err := encoder.Encode(map[string]string{"prompt": r.promptStr}); err != nil {
			_ = f.Close()
			_ = os.RemoveAll(dir)
			return nil, fmt.Errorf("writing prompt dataset: %w", err)
		}
	}
	if err := f.Close(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("closing prompt dataset: %w", err)
	}

	*args = append(*args, "--dataset-path", path, "--custom-output-len", strconv.Itoa(r.maxTokens))
	return func() { _ = os.RemoveAll(dir) }, nil
}

func (r *benchRunner) runInDocker(args []string, structuredDir string) error {
	if err := ensureImagePulled(r.vllmImage); err != nil {
		return err
	}

	dockerArgs := []string{"run", "--rm", "--network", "host", "--entrypoint", "vllm"}

	if absDir, err := filepath.Abs(structuredDir); err == nil {
		dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:%s", absDir, absDir))
	}

	for i, arg := range args {
		if arg == "--dataset-path" && i+1 < len(args) {
			if absDataset, err := filepath.Abs(args[i+1]); err == nil {
				datasetDir := filepath.Dir(absDataset)
				dockerArgs = append(dockerArgs, "-v", fmt.Sprintf("%s:%s", datasetDir, datasetDir))
			}
			break
		}
	}

	dockerArgs = append(dockerArgs, r.vllmImage)
	dockerArgs = append(dockerArgs, args...)

	fmt.Printf("Launching Docker: docker %s\n", shellJoin(dockerArgs))
	cmd := exec.Command("docker", dockerArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// Helpers
func splitBenchmarkEndpoint(rawEndpoint, explicitPath string, apiEndpointChanged bool) (string, string, string, error) {
	parsed, err := url.Parse(rawEndpoint)
	if err != nil {
		return "", "", "", fmt.Errorf("parsing endpoint: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", "", "", fmt.Errorf("endpoint must include scheme and host, e.g. http://localhost:8010")
	}

	apiEndpoint := explicitPath
	if !apiEndpointChanged && parsed.Path != "" && parsed.Path != "/" {
		apiEndpoint = parsed.Path
	}
	if !strings.HasPrefix(apiEndpoint, "/") {
		apiEndpoint = "/" + apiEndpoint
	}

	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	return parsed.Hostname(), port, apiEndpoint, nil
}

func backendForEndpoint(apiEndpoint string) string {
	if strings.Contains(apiEndpoint, "/chat/completions") {
		return "openai-chat"
	}
	return "vllm"
}

func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, arg := range args {
		if strings.ContainsAny(arg, " \t\n\"'") {
			parts[i] = strconv.Quote(arg)
		} else {
			parts[i] = arg
		}
	}
	return strings.Join(parts, " ")
}

func printPerGPUThroughput(resultPath string) {
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return
	}

	var res struct {
		OutputThroughput float64        `json:"output_throughput"`
		Metadata         map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(data, &res); err != nil || res.Metadata == nil {
		return
	}

	tpSize := parseFloatMetadata(res.Metadata, "tp", 1)
	ppSize := parseFloatMetadata(res.Metadata, "pp", 1)

	totalGPUs := tpSize * ppSize
	if totalGPUs > 1 {
		fmt.Printf(
			"\n[Metadata] Normalized Throughput per GPU: %.2f tok/s (TP=%.0f, PP=%.0f)\n",
			res.OutputThroughput/totalGPUs, tpSize, ppSize,
		)
	}
}

func parseFloatMetadata(m map[string]any, key string, defaultVal float64) float64 {
	v, ok := m[key]
	if !ok {
		return defaultVal
	}
	switch val := v.(type) {
	case float64:
		if val > 0 {
			return val
		}
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil && f > 0 {
			return f
		}
	}
	return defaultVal
}

func detectGPUTag() string {
	out, err := exec.Command("nvidia-smi", "--query-gpu=name", "--format=csv,noheader").Output()
	if err != nil {
		return "unknown-gpu"
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return "unknown-gpu"
	}
	name := strings.TrimPrefix(lines[0], "NVIDIA ")
	return strings.TrimSpace(name)
}

func sanitizePath(name string) string {
	r := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-")
	return r.Replace(name)
}

func isVllmAvailable() bool {
	return exec.Command("vllm", "--version").Run() == nil
}

func isDockerAvailable() bool {
	return exec.Command("docker", "--version").Run() == nil
}

func ensureImagePulled(image string) error {
	if exec.Command("docker", "image", "inspect", image).Run() == nil {
		return nil
	}
	fmt.Printf("Pulling %s (this may take a while on first run)...\n", image)
	pull := exec.Command("docker", "pull", image)
	pull.Stdout = os.Stdout
	pull.Stderr = os.Stderr
	return pull.Run()
}