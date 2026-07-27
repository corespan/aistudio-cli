package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/corespan/ai-studio-cli/internal/power"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// serverConfig groups the fields that control server lifecycle and Docker image selection.
type serverConfig struct {
	composeFile string
	keepServer  bool
	timeoutSec  int
	vllmImage   string
}

type benchRunner struct {
	server            serverConfig
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
	openUI            bool
	uiPort            int

	// cost metrics
	costEnabled         bool
	systemBundle        string
	pue                 float64
	pricingFile         string
	avgGPUWattsOverride float64

	// populated at runtime
	gpuWattReadings []float64 // per-GPU draw sampled during the run
	resolvedGPU     string
}

var bench = &benchRunner{}

var benchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Run vLLM's built-in serving benchmark",
	RunE: func(cmd *cobra.Command, args []string) error {
		runner := *bench
		return runner.run(cmd)
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
	cmd.Flags().IntVar(&r.concurrency, "concurrency", 20, "Maximum concurrent benchmark requests")
	cmd.Flags().IntVar(&r.requests, "requests", 200, "Total benchmark prompts to send")
	cmd.Flags().IntVar(&r.maxTokens, "max-tokens", 1024, "Target output token length")
	cmd.Flags().StringVar(&r.dataset, "dataset", "random", "Dataset name for vLLM bench: random, sonnet, sharegpt, custom, hf, etc.")
	cmd.Flags().StringVar(&r.datasetPath, "dataset-path", "", "Dataset path for datasets that require one")
	cmd.Flags().IntVar(&r.warmup, "warmup", 2, "Number of warmup requests")
	cmd.Flags().StringVar(&r.promptStr, "prompt", "", "Single prompt string to repeat for every request")
	cmd.Flags().IntVar(&r.inputLen, "input-len", 512, "Target input token length for synthetic datasets")
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
	cmd.Flags().StringArrayVar(&r.additionalArgs, "vllm-arg", nil, "Additional raw argument for vllm bench serve; can be repeated (advanced: do not interpolate untrusted input)")
	cmd.Flags().StringVar(&r.gpuTag, "gpu-tag", "", "GPU tag for structured result path (auto-detected if empty)")
	// Server lifecycle flags
	cmd.Flags().StringVar(&containerRuntime, "runtime", "auto", "Container engine: auto, docker, or podman")
	cmd.Flags().StringVar(&r.server.vllmImage, "vllm-image", "vllm/vllm-openai:latest", "Container image to use for benchmarking if vllm is not installed locally")
	cmd.Flags().StringVar(&r.server.composeFile, "compose-file", "", "Path to a custom compose file; uses bundled default if server is unreachable and this is unset")
	cmd.Flags().BoolVar(&r.server.keepServer, "keep-server", false, "Leave the vLLM server running after the benchmark completes")
	cmd.Flags().IntVar(&r.server.timeoutSec, "server-timeout", 900, "Maximum seconds to wait for the vLLM server to become ready (default allows for large model load times)")
	// Dashboard
	cmd.Flags().BoolVar(&r.openUI, "ui", true, "On an interactive terminal, launch the results dashboard when the run finishes and print a clickable link (blocks until Ctrl+C); auto-skipped for non-interactive/CI runs, or pass --ui=false")
	cmd.Flags().IntVar(&r.uiPort, "ui-port", 9090, "Port for the dashboard launched by --ui")
	// Cost metrics
	cmd.Flags().BoolVar(&r.costEnabled, "cost", true, "Compute rent-vs-buy cost metrics and embed them in the result JSON")
	cmd.Flags().StringVar(&r.systemBundle, "system-bundle", "", "Named system bundle from the pricing config (e.g. PRU2500_8x5090) to price the owned node as an all-in system")
	cmd.Flags().Float64Var(&r.pue, "pue", 0, "Power Usage Effectiveness for owned-side electricity (0 = use pricing config default)")
	cmd.Flags().StringVar(&r.pricingFile, "pricing-file", "", "Path to an external hardware_pricing.json to override the embedded pricing config")
	cmd.Flags().Float64Var(&r.avgGPUWattsOverride, "avg-gpu-watts", 0, "Override GPU power draw (watts per GPU) for cost energy; 0 = sample live via nvidia-smi")
}

// entry point
func (r *benchRunner) run(cmd *cobra.Command) error {
	effectiveCompose, cleanup, err := serverLifecycle(r.server.composeFile, r.endpoint, r.server.timeoutSec, r.server.keepServer)
	if err != nil {
		return err
	}

	cleanedUp := false
	teardown := func() {
		if !cleanedUp {
			cleanedUp = true
			cleanup()
		}
	}
	defer teardown()

	if err := r.runBenchmark(cmd, effectiveCompose); err != nil {
		return err
	}

	teardown()

	if r.openUI && term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Printf("\nLaunching results dashboard (Ctrl+C to stop)...\n")
		if err := serveBenchUI(r.resultDir, r.uiPort, false); err != nil {
			fmt.Fprintf(os.Stderr, "dashboard failed to start: %v\n", err)
		}
	}
	return nil
}

func (r *benchRunner) runBenchmark(cmd *cobra.Command, composePath string) error {
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

	r.ensureTokenizer(composePath, modelName)

	gpu := r.gpuTag
	if gpu == "" {
		gpu = detectGPUTag()
	}
	r.resolvedGPU = gpu

	timestamp := time.Now().Format("2006-01-02T15-04-05")
	structuredDir := filepath.Join(r.resultDir, sanitizePath(modelName), sanitizePath(gpu), timestamp)
	if abs, err := filepath.Abs(structuredDir); err == nil {
		structuredDir = abs
	}

	args := r.buildArgs(host, port, apiEndpoint, modelName, datasetName, structuredDir)

	cleanup, err := r.applyPromptDataset(&args)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := os.MkdirAll(structuredDir, 0755); err != nil {
		return fmt.Errorf("creating result directory: %w", err)
	}

	// Sample GPU power during the run so the cost engine can charge realistic
	// owned-side electricity (no telemetry stack exists otherwise).
	var sampler *power.Sampler
	if r.costEnabled && r.avgGPUWattsOverride <= 0 {
		sampler = power.NewSampler(2 * time.Second)
		sampler.Start()
	}
	runErr := r.execute(cmd.Context(), args, structuredDir)
	if sampler != nil {
		r.gpuWattReadings = sampler.Stop()
	}
	if runErr != nil {
		return runErr
	}

	resultPath := filepath.Join(structuredDir, r.resultFilename)
	fmt.Printf("\nResult saved to: %s\n", resultPath)

	if err := r.patchResultJSON(resultPath, composePath); err != nil {
		fmt.Printf("Warning: could not enrich result JSON: %v\n", err)
	}

	printPerGPUThroughput(resultPath)
	return nil
}

func (r *benchRunner) ensureTokenizer(composePath, modelName string) {
	if composePath == "" || hasTokenizerArg(r.additionalArgs) {
		return
	}
	modelPath := ExtractModelPath(composePath)
	if modelPath == "" || modelPath == modelName {
		return
	}
	r.additionalArgs = append(r.additionalArgs, "--tokenizer="+modelPath)
	fmt.Printf("Served model %q is an alias; using tokenizer %q from compose file.\n", modelName, modelPath)
}

func hasTokenizerArg(args []string) bool {
	for _, a := range args {
		if a == "--tokenizer" || strings.HasPrefix(a, "--tokenizer=") {
			return true
		}
	}
	return false
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
	if err != nil {
		return "", fmt.Errorf("failed to auto-detect model: %w\n  Hint: pass --model explicitly", err)
	}
	if len(models) == 0 {
		return "", fmt.Errorf("no models returned by vLLM at %s; pass --model explicitly", baseEndpoint)
	}
	fmt.Printf("Detected model: %s\n", models[0])
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

	if modelName != "" {
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

func (r *benchRunner) execute(ctx context.Context, args []string, structuredDir string) error {
	if isVllmAvailable() {
		fmt.Printf("Launching: vllm %s\n", shellJoin(args))
		cmd := exec.CommandContext(ctx, "vllm", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("vllm bench serve failed: %w", err)
		}
		return nil
	}

	engine := currentEngine()
	if isContainerEngineAvailable(engine) {
		return r.runInContainer(ctx, engine, args, structuredDir)
	}

	return fmt.Errorf("neither 'vllm' nor a container engine (%s) was found; install vLLM locally or install %s", engine.cli(), engine.cli())
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

// runInContainer runs the vLLM benchmark client in a container when vLLM is not
// installed locally. The client is a plain HTTP load generator (it talks to the
// already-running server endpoint), so it does not request GPU access itself.
func (r *benchRunner) runInContainer(ctx context.Context, engine containerEngine, args []string, resultDir string) error {
	if err := ensureImagePulled(ctx, engine, r.server.vllmImage); err != nil {
		return err
	}

	runArgs := []string{"run", "--rm", "--network", "host", "--entrypoint", "vllm"}
	if engine.isPodman() {
		// Avoid SELinux denials on bind-mounted dirs (no-op on AppArmor hosts).
		runArgs = append(runArgs, "--security-opt=label=disable")
	}

	if absDir, err := filepath.Abs(resultDir); err == nil {
		runArgs = append(runArgs, "-v", fmt.Sprintf("%s:%s", absDir, absDir))
	}

	for i, arg := range args {
		if arg == "--dataset-path" && i+1 < len(args) {
			if absDataset, err := filepath.Abs(args[i+1]); err == nil {
				datasetDir := filepath.Dir(absDataset)
				runArgs = append(runArgs, "-v", fmt.Sprintf("%s:%s", datasetDir, datasetDir))
			}
			break
		}
	}

	runArgs = append(runArgs, r.server.vllmImage)
	runArgs = append(runArgs, args...)

	fmt.Printf("Launching container: %s %s\n", engine.cli(), shellJoin(runArgs))

	cmd := exec.CommandContext(ctx, engine.cli(), runArgs...)
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
	switch {
	case strings.Contains(apiEndpoint, "/chat/completions"):
		return "openai-chat"
	case strings.Contains(apiEndpoint, "/v1/completions"):
		return "openai"
	default:
		return "vllm"
	}
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
		OutputThroughput     float64     `json:"output_throughput"`
		TotalTokenThroughput float64     `json:"total_token_throughput"`
		ModelConfig          ModelConfig `json:"model_config"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return
	}

	tpSize := float64(res.ModelConfig.TensorParallel)
	ppSize := float64(res.ModelConfig.PipelineParallel)
	if tpSize == 0 {
		tpSize = 1
	}
	if ppSize == 0 {
		ppSize = 1
	}

	totalGPUs := tpSize * ppSize
	if totalGPUs > 1 {
		fmt.Printf(
			"\n[Per-GPU] Total tok/s: %.2f  Output tok/s: %.2f  |  Total GPUs: %.0f (TP=%.0f × PP=%.0f)\n",
			res.TotalTokenThroughput/totalGPUs, res.OutputThroughput/totalGPUs,
			totalGPUs, tpSize, ppSize,
		)
	}
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
	r := strings.NewReplacer("/", "-", "\\", "-", ":", "-", " ", "-", "..", "")
	return r.Replace(name)
}

func isVllmAvailable() bool {
	return exec.Command("vllm", "--version").Run() == nil
}

func isContainerEngineAvailable(engine containerEngine) bool {
	return exec.Command(engine.cli(), "--version").Run() == nil
}

func ensureImagePulled(ctx context.Context, engine containerEngine, image string) error {
	if exec.CommandContext(ctx, engine.cli(), "image", "inspect", image).Run() == nil {
		return nil
	}
	fmt.Printf("Pulling %s (this may take a while on first run)...\n", image)
	pull := exec.CommandContext(ctx, engine.cli(), "pull", image)
	pull.Stdout = os.Stdout
	pull.Stderr = os.Stderr
	return pull.Run()
}
