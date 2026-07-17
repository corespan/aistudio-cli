// Package cost ports the AI-ML workbench CostEngine to Go for the aistudio-cli
// vLLM benchmark flow. It computes LLM cost-per-1M-tokens (3:1 output weighting)
// and a rent-vs-buy ladder that reproduces the CoreSpan RTX 5090 / PRU-2500 blog
// findings. The owned side is "fully loaded" (amortized CapEx + electricity) so it
// is a fair comparison against rental rates, which already bundle power.
package cost

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
)

//go:embed pricing.json
var embeddedPricing []byte

// ---- Config schema (mirrors hardware_pricing.json) ----

type HWSpec struct {
	CapexUSD      float64 `json:"capex_usd"`
	LifespanYears float64 `json:"lifespan_years"`
	DefaultWatts  float64 `json:"default_watts,omitempty"`
}

type BundleSpec struct {
	Description   string  `json:"description"`
	CapexUSD      float64 `json:"capex_usd"`
	LifespanYears float64 `json:"lifespan_years"`
	NumGPUs       int     `json:"num_gpus"`
	GPUModel      string  `json:"gpu_model"`
}

type Tier struct {
	USDHr float64 `json:"usd_hr"`
	Label string  `json:"label"`
}

type MarketRate struct {
	HyperscalerSKU       string          `json:"hyperscaler_sku"`
	RentalHourlyUSD      float64         `json:"rental_hourly_usd"`
	NeocloudBlendedUSD   float64         `json:"neocloud_blended_hourly_usd"`
	HyperscalerHourlyUSD float64         `json:"hyperscaler_hourly_usd"`
	RentalTiers          map[string]Tier `json:"rental_tiers"`
}

type Config struct {
	Meta struct {
		LastUpdatedUTC    string  `json:"last_updated_utc"`
		ElectricityKWhUSD float64 `json:"electricity_kwh_usd"`
		PUE               float64 `json:"pue"`
		ElectricitySource string  `json:"electricity_source"`
	} `json:"meta"`
	CorespanInventory struct {
		GPUs          map[string]HWSpec     `json:"gpus"`
		SystemBundles map[string]BundleSpec `json:"system_bundles"`
	} `json:"corespan_inventory"`
	MarketRates struct {
		GPUs map[string]MarketRate `json:"gpus"`
	} `json:"market_rates"`
}

// ---- Inputs / outputs ----

type InfraConfig struct {
	SystemBundle string
	PUE          float64 // 0 => use config default
}

type LLMInput struct {
	GPUName           string
	NumGPUs           int     // GPUs the workload actually used (e.g. TP*PP)
	DurationSeconds   float64
	AvgPowerWatts     float64 // per-GPU estimate; used when no measured total is available
	MeasuredNodeWatts float64 // total node draw measured live; when > 0 it wins over AvgPowerWatts
	TotalInputTokens  float64
	TotalOutputTokens float64
	Infra             InfraConfig
}

type TokenCost struct {
	Blended float64 `json:"blended_cost_per_1m_tokens_usd"`
	Input   float64 `json:"input_cost_per_1m_tokens_usd"`
	Output  float64 `json:"output_cost_per_1m_tokens_usd"`
}

type LadderTier struct {
	Tier         string  `json:"tier"`
	Label        string  `json:"label"`
	RentalHourly float64 `json:"rental_hourly_usd"`
	RentalTotal  float64 `json:"rental_total_usd"`
	SavingsVsBuy float64 `json:"savings_usd_vs_buy"`
	Verdict      string  `json:"verdict"`
}

type Ladder struct {
	OwnedCapexOnly   float64      `json:"owned_capex_only_usd"`
	OwnedEnergyCost  float64      `json:"owned_energy_cost_usd"`
	OwnedFullyLoaded float64      `json:"owned_fully_loaded_usd"`
	EffectiveNumGPUs int          `json:"effective_num_gpus"`
	WindowHours      float64      `json:"window_hours"`
	Tiers            []LadderTier `json:"tiers"`
}

type LLMResult struct {
	GPUDetected           string    `json:"gpu_detected"`
	PricingSourceDate     string    `json:"pricing_source_date"`
	WorkloadDurationHours float64   `json:"workload_duration_hours"`
	AvgGPUPowerWatts      float64   `json:"avg_gpu_power_watts"`
	OwnedCapexOnlyUSD     float64   `json:"owned_capex_only_usd"`
	OwnedEnergyCostUSD    float64   `json:"owned_energy_cost_usd"`
	OwnedFullyLoadedUSD   float64   `json:"owned_fully_loaded_usd"`
	BaselineSavingsUSD    float64   `json:"baseline_savings_usd"`
	CorespanInfra         TokenCost `json:"corespan_infra"`
	Rental                TokenCost `json:"rental"`
	Hyperscaler           TokenCost `json:"hyperscaler"`
	RentVsBuy             Ladder    `json:"rent_vs_buy_analysis"`
}

// ---- Engine ----

type Engine struct {
	cfg Config
}

// NewEngine loads the embedded pricing config, or an external file when path is non-empty.
func NewEngine(externalPath string) (*Engine, error) {
	raw := embeddedPricing
	if externalPath != "" {
		b, err := os.ReadFile(externalPath)
		if err != nil {
			return nil, fmt.Errorf("reading pricing file %q: %w", externalPath, err)
		}
		raw = b
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, fmt.Errorf("parsing pricing config: %w", err)
	}
	if len(cfg.CorespanInventory.GPUs) == 0 {
		return nil, fmt.Errorf("pricing config has no GPU inventory")
	}
	return &Engine{cfg: cfg}, nil
}

func (e *Engine) gpuKey(name string) string {
	u := strings.ToUpper(name)
	switch {
	case strings.Contains(u, "A100"):
		return "A100"
	case strings.Contains(u, "H100"):
		return "H100"
	case strings.Contains(u, "5090"):
		return "RTX 5090"
	case strings.Contains(u, "5070"):
		return "RTX 5070"
	case strings.Contains(u, "T4"):
		return "Tesla T4"
	default:
		return "DEFAULT"
	}
}

func amortizeHourly(capex, lifespanYears float64) float64 {
	if lifespanYears <= 0 {
		return 0
	}
	return capex / (lifespanYears * 365 * 24)
}

// DefaultWatts returns the fallback per-GPU wattage for a GPU when live sampling
// is unavailable.
func (e *Engine) DefaultWatts(gpuName string) float64 {
	if spec, ok := e.cfg.CorespanInventory.GPUs[e.gpuKey(gpuName)]; ok && spec.DefaultWatts > 0 {
		return spec.DefaultWatts
	}
	if spec, ok := e.cfg.CorespanInventory.GPUs["DEFAULT"]; ok && spec.DefaultWatts > 0 {
		return spec.DefaultWatts
	}
	return 400
}

func (e *Engine) resolvePUE(infra InfraConfig) float64 {
	if infra.PUE > 0 {
		return infra.PUE
	}
	if e.cfg.Meta.PUE > 0 {
		return e.cfg.Meta.PUE
	}
	return 1.0
}

// ownedEnergyCost is charged only to the owned side (rental rates already bundle
// power). It takes the *total* node watts, not per-GPU, so live measurements of a
// partially-used node are billed exactly as drawn.
func (e *Engine) ownedEnergyCost(totalWatts, hours float64, infra InfraConfig) float64 {
	return (totalWatts / 1000.0) * e.resolvePUE(infra) * hours * e.cfg.Meta.ElectricityKWhUSD
}

// resolveCorespanCapex returns the amortized owned CapEx for the window and the
// effective GPU count. A named system bundle contributes its *per-GPU* all-in
// price, scaled by the GPUs the workload actually used — so a 4-GPU run against
// an 8-GPU bundle prices 4 GPUs, while a full 8-GPU run reproduces the whole-node
// bundle price exactly.
func (e *Engine) resolveCorespanCapex(gpuKey string, numGPUs int, hours float64, infra InfraConfig) (float64, int) {
	if numGPUs < 1 {
		numGPUs = 1
	}
	if infra.SystemBundle != "" {
		if b, ok := e.cfg.CorespanInventory.SystemBundles[infra.SystemBundle]; ok {
			perGPU := b.CapexUSD
			if b.NumGPUs > 0 {
				perGPU = b.CapexUSD / float64(b.NumGPUs)
			}
			return amortizeHourly(perGPU, b.LifespanYears) * float64(numGPUs) * hours, numGPUs
		}
	}
	gpu, ok := e.cfg.CorespanInventory.GPUs[gpuKey]
	if !ok {
		gpu = e.cfg.CorespanInventory.GPUs["DEFAULT"]
	}
	return amortizeHourly(gpu.CapexUSD, gpu.LifespanYears) * float64(numGPUs) * hours, numGPUs
}

func (e *Engine) marketRate(gpuKey string) MarketRate {
	if m, ok := e.cfg.MarketRates.GPUs[gpuKey]; ok {
		return m
	}
	return e.cfg.MarketRates.GPUs["DEFAULT"]
}

// coreLadder builds the rent-vs-buy ladder from already-resolved owned CapEx and
// energy. Tiers are ordered ascending by rate for a stable, readable ladder.
func (e *Engine) coreLadder(gpuKey string, eff int, hours, capex, energy float64) Ladder {
	fullyLoaded := capex + energy
	market := e.marketRate(gpuKey)

	keys := make([]string, 0, len(market.RentalTiers))
	for k := range market.RentalTiers {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return market.RentalTiers[keys[i]].USDHr < market.RentalTiers[keys[j]].USDHr
	})

	tiers := make([]LadderTier, 0, len(keys))
	for _, k := range keys {
		t := market.RentalTiers[k]
		rentalTotal := t.USDHr * float64(eff) * hours
		savings := rentalTotal - fullyLoaded
		verdict := "rent"
		if savings > 0 {
			verdict = "buy"
		}
		tiers = append(tiers, LadderTier{
			Tier:         k,
			Label:        t.Label,
			RentalHourly: t.USDHr,
			RentalTotal:  round(rentalTotal, 2),
			SavingsVsBuy: round(savings, 2),
			Verdict:      verdict,
		})
	}

	return Ladder{
		OwnedCapexOnly:   round(capex, 2),
		OwnedEnergyCost:  round(energy, 2),
		OwnedFullyLoaded: round(fullyLoaded, 2),
		EffectiveNumGPUs: eff,
		WindowHours:      round(hours, 4),
		Tiers:            tiers,
	}
}

// RentVsBuyLadder is the per-GPU convenience entrypoint: pass average watts per
// GPU and it charges avgWattsPerGPU * (GPUs used) of power.
func (e *Engine) RentVsBuyLadder(gpuKey string, numGPUs int, hours, avgWattsPerGPU float64, infra InfraConfig) Ladder {
	capex, eff := e.resolveCorespanCapex(gpuKey, numGPUs, hours, infra)
	energy := e.ownedEnergyCost(avgWattsPerGPU*float64(eff), hours, infra)
	return e.coreLadder(gpuKey, eff, hours, capex, energy)
}

// CalculateLLM computes token-cost economics plus the rent-vs-buy ladder for a run.
func (e *Engine) CalculateLLM(in LLMInput) LLMResult {
	gpuKey := e.gpuKey(in.GPUName)
	hours := in.DurationSeconds / 3600.0

	capex, eff := e.resolveCorespanCapex(gpuKey, in.NumGPUs, hours, in.Infra)

	// Prefer measured total node power; otherwise estimate from per-GPU watts.
	totalWatts := in.MeasuredNodeWatts
	if totalWatts <= 0 {
		totalWatts = in.AvgPowerWatts * float64(eff)
	}
	energy := e.ownedEnergyCost(totalWatts, hours, in.Infra)
	corespanTotal := capex + energy

	market := e.marketRate(gpuKey)
	rentalTotal := market.RentalHourlyUSD * float64(eff) * hours
	hyperTotal := market.HyperscalerHourlyUSD * float64(eff) * hours

	totalTokens := in.TotalInputTokens + in.TotalOutputTokens
	weighted := func(totalCost float64) TokenCost {
		if totalTokens <= 0 {
			return TokenCost{}
		}
		blended := totalCost / totalTokens * 1e6
		wd := in.TotalInputTokens + 3*in.TotalOutputTokens // 3:1 output-to-input weighting
		if wd <= 0 {
			return TokenCost{Blended: round(blended, 4)}
		}
		baseInput := totalCost / wd
		return TokenCost{
			Blended: round(blended, 4),
			Input:   round(baseInput*1e6, 4),
			Output:  round(baseInput*3*1e6, 4),
		}
	}

	perGPUWatts := 0.0
	if eff > 0 {
		perGPUWatts = totalWatts / float64(eff)
	}

	return LLMResult{
		GPUDetected:           in.GPUName,
		PricingSourceDate:     e.cfg.Meta.LastUpdatedUTC,
		WorkloadDurationHours: round(hours, 6),
		AvgGPUPowerWatts:      round(perGPUWatts, 2),
		OwnedCapexOnlyUSD:     round(capex, 6),
		OwnedEnergyCostUSD:    round(energy, 6),
		OwnedFullyLoadedUSD:   round(corespanTotal, 6),
		BaselineSavingsUSD:    round(hyperTotal-corespanTotal, 6),
		CorespanInfra:         weighted(corespanTotal),
		Rental:                weighted(rentalTotal),
		Hyperscaler:           weighted(hyperTotal),
		RentVsBuy:             e.coreLadder(gpuKey, eff, hours, capex, energy),
	}
}

func round(v float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(v*p) / p
}
