package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

func (r *benchRunner) patchResultJSON(resultPath, composePath string) error {
	data, err := os.ReadFile(resultPath)
	if err != nil {
		return fmt.Errorf("reading result for patching: %w", err)
	}

	// UseNumber keeps integer fields (token counts) exact instead of
	// round-tripping them through float64 on re-marshal.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]interface{}
	if err := dec.Decode(&raw); err != nil {
		return fmt.Errorf("parsing result JSON: %w", err)
	}

	cfg := r.resolveModelConfig(composePath)
	raw["model_config"] = cfg

	if existing := toFloat(raw["total_token_throughput"]); existing <= 0 {
		outTP := toFloat(raw["output_throughput"])
		inTP := toFloat(raw["input_throughput"])
		raw["total_token_throughput"] = outTP + inTP
	}

	if r.costEnabled {
		if err := r.injectCostMetrics(raw, cfg); err != nil {
			fmt.Printf("  [cost] warning: %v\n", err)
		} else {
			fmt.Println("  [cost] rent-vs-buy metrics added to result JSON")
		}
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling patched result: %w", err)
	}

	// Write atomically to prevent data loss on interruption
	tmpFile, err := os.CreateTemp(filepath.Dir(resultPath), ".patch-*.json")
	if err != nil {
		return fmt.Errorf("creating temp file for patched result: %w", err)
	}

	if _, err := tmpFile.Write(out); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return fmt.Errorf("writing patched result: %w", err)
	}
	tmpFile.Close()

	if err := os.Rename(tmpFile.Name(), resultPath); err != nil {
		os.Remove(tmpFile.Name())
		return fmt.Errorf("committing patched result: %w", err)
	}
	return nil
}

// resolveModelConfig determines model config from compose file, or falls back to defaults.
func (r *benchRunner) resolveModelConfig(composePath string) ModelConfig {
	if composePath != "" {
		cfg, warnings := ExtractModelConfig(composePath)
		for _, w := range warnings {
			fmt.Printf("  [model-config] warning: %s\n", w)
		}
		fmt.Printf("  [model-config] source: compose file (%s)\n", composePath)
		return cfg
	}

	// No compose file — use defaults and tell the user how to fix it.
	fmt.Println("  [model-config] warning: no compose file provided; recording defaults (tp=1, pp=1).")
	fmt.Println("  [model-config] hint: pass --compose-file to record accurate serving config.")
	return ModelConfig{TensorParallel: 1, PipelineParallel: 1}
}

func toFloat(v interface{}) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case int64:
		return float64(val)
	case json.Number:
		if f, err := val.Float64(); err == nil {
			return f
		}
	}
	return 0
}
