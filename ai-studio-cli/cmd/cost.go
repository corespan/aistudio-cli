package cmd

import (
	"fmt"

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

	// Power precedence: explicit per-GPU override > live-measured node total >
	// per-GPU default from the pricing config.
	var perGPUWatts, measuredNodeWatts float64
	switch {
	case r.avgGPUWattsOverride > 0:
		perGPUWatts = r.avgGPUWattsOverride
	case r.measuredNodeWatts > 0:
		measuredNodeWatts = r.measuredNodeWatts
	default:
		perGPUWatts = engine.DefaultWatts(gpuName)
	}

	result := engine.CalculateLLM(cost.LLMInput{
		GPUName:           gpuName,
		NumGPUs:           numGPUs,
		DurationSeconds:   duration,
		AvgPowerWatts:     perGPUWatts,
		MeasuredNodeWatts: measuredNodeWatts,
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
