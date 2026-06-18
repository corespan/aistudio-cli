package cmd

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModelConfig holds the serving configuration extracted from docker-compose.
type ModelConfig struct {
	TensorParallel   int    `json:"tp,omitempty"`
	PipelineParallel int    `json:"pp,omitempty"`
	Quantization     string `json:"quantization,omitempty"`
	Dtype            string `json:"dtype,omitempty"`
	MaxModelLen      int    `json:"max_model_len,omitempty"`
	GPUMemUtil       string `json:"gpu_memory_utilization,omitempty"`
}

type composeService struct {
	Image       string      `yaml:"image"`
	Command     interface{} `yaml:"command"`
	Environment interface{} `yaml:"environment"`
}

type composeConfig struct {
	Services map[string]composeService `yaml:"services"`
}

// ExtractModelConfig reads a docker-compose file and returns model config.
func ExtractModelConfig(composePath string) (ModelConfig, []string) {
	cfg := ModelConfig{TensorParallel: 1, PipelineParallel: 1}

	if composePath == "" {
		return cfg, []string{"no compose file path provided; using defaults"}
	}

	data, err := os.ReadFile(composePath)
	if err != nil {
		return cfg, []string{fmt.Sprintf("reading compose file %s: %v", composePath, err)}
	}

	var cf composeConfig
	if err := yaml.Unmarshal(data, &cf); err != nil {
		return cfg, []string{fmt.Sprintf("failed to parse compose YAML: %v", err)}
	}

	if len(cf.Services) == 0 {
		return cfg, []string{"no services found in compose file"}
	}

	svc, _, found := findVLLMService(cf.Services)
	if !found {
		svc = cf.Services[sortedServiceNames(cf.Services)[0]]
	}

	var warnings []string
	env := flattenEnv(svc.Environment)
	cmd := flattenCommand(svc.Command)

	// vLLM itself, where --flag beats env var).
	envCfg, envWarns := parseEnvVars(env)
	cmdCfg, cmdWarns := parseCLIFlags(cmd)
	warnings = append(warnings, envWarns...)
	warnings = append(warnings, cmdWarns...)

	// Merge: cmd wins over env
	cfg.TensorParallel = coalesceInt(cmdCfg.TensorParallel, envCfg.TensorParallel, 1)
	cfg.PipelineParallel = coalesceInt(cmdCfg.PipelineParallel, envCfg.PipelineParallel, 1)
	cfg.Quantization = coalesceStr(cmdCfg.Quantization, envCfg.Quantization)
	cfg.Dtype = coalesceStr(cmdCfg.Dtype, envCfg.Dtype)
	cfg.MaxModelLen = coalesceInt(cmdCfg.MaxModelLen, envCfg.MaxModelLen, 0)
	cfg.GPUMemUtil = coalesceStr(cmdCfg.GPUMemUtil, envCfg.GPUMemUtil)

	return cfg, warnings
}

func sortedServiceNames(services map[string]composeService) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func findVLLMService(services map[string]composeService) (composeService, string, bool) {
	// A service literally named "vllm" is the serving one (a sibling
	// "benchmark" service often shares the same image but has no command).
	if svc, ok := services["vllm"]; ok {
		return svc, "vllm", true
	}

	// Otherwise pick a vllm-looking service, preferring one that actually
	// carries a command (the flags we need to parse) over an empty one.
	var fallback composeService
	var fallbackName string
	found := false
	for _, name := range sortedServiceNames(services) {
		svc := services[name]
		cmdParts := flattenCommand(svc.Command)
		cmd := strings.ToLower(strings.Join(cmdParts, " "))
		img := strings.ToLower(svc.Image)
		if !strings.Contains(img, "vllm") &&
			!strings.Contains(cmd, "vllm") &&
			!strings.Contains(cmd, "vllm.entrypoints") {
			continue
		}
		if len(cmdParts) > 0 {
			return svc, name, true
		}
		if !found {
			fallback, fallbackName, found = svc, name, true
		}
	}
	return fallback, fallbackName, found
}

func parseEnvVars(env map[string]string) (ModelConfig, []string) {
	var cfg ModelConfig
	var warns []string

	readInt := func(key string, target *int) {
		if v, ok := env[key]; ok && v != "" {
			if isEnvVarSubstitution(v) {
				warns = append(warns, fmt.Sprintf("%s=%q is an unresolved shell variable; skipping", key, v))
				return
			}
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				warns = append(warns, fmt.Sprintf("%s=%q is not a valid integer: %v", key, v, err))
			} else if n > 0 {
				*target = n
			}
		}
	}

	readStr := func(keys []string, target *string, lower bool) {
		for _, key := range keys {
			if v, ok := env[key]; ok && v != "" {
				if isEnvVarSubstitution(v) {
					warns = append(warns, fmt.Sprintf("%s=%q is an unresolved shell variable; skipping", key, v))
					return
				}
				if lower {
					*target = strings.ToLower(v)
				} else {
					*target = v
				}
				return // first matching key wins
			}
		}
	}

	readInt("TENSOR_PARALLEL_SIZE", &cfg.TensorParallel)
	readInt("PIPELINE_PARALLEL_SIZE", &cfg.PipelineParallel)
	readInt("MAX_MODEL_LEN", &cfg.MaxModelLen)
	if cfg.MaxModelLen == 0 {
		readInt("VLLM_MAX_MODEL_LEN", &cfg.MaxModelLen)
	}

	readStr([]string{"QUANTIZATION", "VLLM_QUANTIZATION"}, &cfg.Quantization, true)
	readStr([]string{"DTYPE", "VLLM_DTYPE"}, &cfg.Dtype, true)
	readStr([]string{"GPU_MEMORY_UTILIZATION", "VLLM_GPU_MEMORY_UTILIZATION"}, &cfg.GPUMemUtil, false)

	return cfg, warns
}

func parseCLIFlags(tokens []string) (ModelConfig, []string) {
	var cfg ModelConfig
	var warns []string

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]

		// Split --flag=value form into two virtual tokens.
		flagName, inlineVal, hasInline := strings.Cut(tok, "=")
		if !strings.HasPrefix(flagName, "-") {
			continue
		}

		nextVal := func() string {
			if hasInline {
				return inlineVal
			}
			if i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "--") {
				i++
				return tokens[i]
			}
			return ""
		}

		parseInt := func(target *int) {
			v := nextVal()
			if isEnvVarSubstitution(v) {
				warns = append(warns, fmt.Sprintf("%s=%q is an unresolved shell variable; skipping", flagName, v))
				return
			}
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err != nil {
				warns = append(warns, fmt.Sprintf("%s=%q: %v", flagName, v, err))
			} else if n > 0 {
				*target = n
			}
		}

		parseStr := func(target *string, lower bool) {
			v := nextVal()
			if v == "" || isEnvVarSubstitution(v) {
				if isEnvVarSubstitution(v) {
					warns = append(warns, fmt.Sprintf("%s=%q is an unresolved shell variable; skipping", flagName, v))
				}
				return
			}
			if lower {
				*target = strings.ToLower(v)
			} else {
				*target = v
			}
		}

		switch flagName {
		case "--tensor-parallel-size":
			parseInt(&cfg.TensorParallel)
		case "--pipeline-parallel-size":
			parseInt(&cfg.PipelineParallel)
		case "--quantization", "-q":
			parseStr(&cfg.Quantization, true)
		case "--dtype":
			parseStr(&cfg.Dtype, true)
		case "--max-model-len":
			parseInt(&cfg.MaxModelLen)
		case "--gpu-memory-utilization":
			parseStr(&cfg.GPUMemUtil, false)
		}
	}

	return cfg, warns
}

// flattenEnv normalises list-form and map-form environment blocks.
func flattenEnv(raw interface{}) map[string]string {
	out := make(map[string]string)
	if raw == nil {
		return out
	}
	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			k, val, _ := strings.Cut(s, "=")
			out[strings.TrimSpace(k)] = strings.TrimSpace(val)
		}
	case map[string]interface{}:
		for k, val := range v {
			if val == nil {
				out[k] = ""
				continue
			}
			out[k] = strings.TrimSpace(fmt.Sprintf("%v", val))
		}
	}
	return out
}

// flattenCommand extracts a YAML list or scalar command into a string slice.
func flattenCommand(raw interface{}) []string {
	if raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case string:
		return splitCommandLine(v)
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, fmt.Sprintf("%v", item))
		}
		return parts
	}
	return nil
}

func splitCommandLine(s string) []string {
	var args []string
	var buf strings.Builder
	var quote rune // 0 when not inside a quoted section
	inToken := false

	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				buf.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if inToken {
				args = append(args, buf.String())
				buf.Reset()
				inToken = false
			}
		default:
			buf.WriteRune(r)
			inToken = true
		}
	}
	if inToken {
		args = append(args, buf.String())
	}
	return args
}

// isEnvVarSubstitution reports whether a value looks like ${VAR} or $VAR.
var envVarRe = regexp.MustCompile(`^\$\{?[A-Za-z_][A-Za-z0-9_]*(?::-[^}]*)?\}?$`)

func isEnvVarSubstitution(s string) bool {
	return envVarRe.MatchString(strings.TrimSpace(s))
}

func coalesceInt(a, b, fallback int) int {
	if a > 0 {
		return a
	}
	if b > 0 {
		return b
	}
	return fallback
}

func coalesceStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
