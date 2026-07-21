package cmd

import (
	"fmt"
	"sort"

	"github.com/corespan/ai-studio-cli/internal/cost"
)

// injectCostMetrics computes rent-vs-buy cost metrics from the benchmark result
// and adds them under raw["cost_metrics"]. It reads token/duration figures from
// the vLLM result, GPU count from the model config (TP*PP), and owned-side power
// from the live sample (falling back to the pricing config's default wattage).
func (r *benchRunner) injectCostMetrics(raw map[string]interface{}, cfg ModelConfig) error {
	engine, err := cost.NewEngine(r.pricingFile)
	if err != nil {
		return fmt.Errorf("loading pricing config: %w", err)
	}

	duration := toFloat(raw["duration"])
	inTok := toFloat(raw["total_input_tokens"])
	outTok := toFloat(raw["total_output_tokens"])
	if duration <= 0 || (inTok+outTok) <= 0 {
		return fmt.Errorf("result missing duration/token fields; skipping cost metrics")
	}

	numGPUs := cfg.TensorParallel * cfg.PipelineParallel
	if numGPUs < 1 {
		numGPUs = 1
	}

	gpuName := r.resolvedGPU
	if gpuName == "" {
		gpuName = detectGPUTag()
	}

	// Per-GPU power precedence: explicit override > live sample of the GPUs in
	// use > per-GPU default from the pricing config.
	perGPUWatts := r.avgGPUWattsOverride
	if perGPUWatts <= 0 {
		perGPUWatts = avgWattsOfUsedGPUs(r.gpuWattReadings, numGPUs)
	}
	if perGPUWatts <= 0 {
		perGPUWatts = engine.DefaultWatts(gpuName)
	}

	result := engine.CalculateLLM(cost.LLMInput{
		GPUName:           gpuName,
		NumGPUs:           numGPUs,
		DurationSeconds:   duration,
		AvgPowerWatts:     perGPUWatts,
		TotalInputTokens:  inTok,
		TotalOutputTokens: outTok,
		Infra: cost.InfraConfig{
			SystemBundle: r.systemBundle,
			PUE:          r.pue,
		},
	})

	raw["cost_metrics"] = result
	return nil
}

// avgWattsOfUsedGPUs returns the mean per-GPU draw of the busiest `used` GPUs.
// The GPUs doing the work draw the most power, so ranking by draw and taking the
// top N approximates "the GPUs in use" without needing device-ID mapping — and
// keeps electricity scoped to the same GPUs the CapEx is scoped to.
func avgWattsOfUsedGPUs(readings []float64, used int) float64 {
	if len(readings) == 0 || used <= 0 {
		return 0
	}
	sorted := append([]float64(nil), readings...)
	sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))
	if used > len(sorted) {
		used = len(sorted)
	}
	var sum float64
	for i := 0; i < used; i++ {
		sum += sorted[i]
	}
	return sum / float64(used)
}
