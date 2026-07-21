package cost

import (
	"math"
	"testing"
)

// Golden test: the Go cost engine must reproduce the published rent-vs-buy blog
// findings. Blog: https://www.corespan.ai/resources/blog/rent-or-buy-rtx-5090-pru-2500
// Scenario: PRU 2500 + 8x RTX 5090, 24/7 over 3 years (210,240 GPU-hours), $99,999 all-in.

const (
	years       = 3.0
	windowHours = years * 365 * 24 // 26,280
	avgWatts    = 550.0            // ~96% of the 575W TDP under sustained inference
	bundleCapex = 99999.0
	bundle      = "PRU2500_8x5090"
)

// Published savings (positive = buying wins), CapEx-only basis, to the dollar.
var blogSavingsCapexOnly = map[string]float64{
	"spot":             -43234, // rent wins
	"reserved":         -18005, // rent wins
	"dedicated_median": 36657,  // buy wins
	"dedicated_high":   85012,  // buy wins
	"premium_secure":   108139, // buy wins
}

func testEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine("")
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func testLadder(t *testing.T) Ladder {
	t.Helper()
	e := testEngine(t)
	if got := e.gpuKey("NVIDIA GeForce RTX 5090"); got != "RTX 5090" {
		t.Fatalf("gpuKey = %q, want RTX 5090", got)
	}
	return e.RentVsBuyLadder("RTX 5090", 8, windowHours, avgWatts, InfraConfig{SystemBundle: bundle})
}

func tierByKey(l Ladder) map[string]LadderTier {
	m := make(map[string]LadderTier, len(l.Tiers))
	for _, t := range l.Tiers {
		m[t.Tier] = t
	}
	return m
}

func TestBundleCapexAndGPUCount(t *testing.T) {
	l := testLadder(t)
	if l.EffectiveNumGPUs != 8 {
		t.Errorf("effective GPUs = %d, want 8", l.EffectiveNumGPUs)
	}
	if math.Abs(l.OwnedCapexOnly-bundleCapex) > 0.01 {
		t.Errorf("owned capex = %v, want %v", l.OwnedCapexOnly, bundleCapex)
	}
	perGPUHr := bundleCapex / (8 * windowHours)
	if math.Abs(perGPUHr-0.4756) > 0.001 {
		t.Errorf("per-GPU-hr = %v, want ~0.4756", perGPUHr)
	}
}

func TestCapexOnlyReproducesBlogToTheDollar(t *testing.T) {
	l := testLadder(t)
	byTier := tierByKey(l)
	for tier, want := range blogSavingsCapexOnly {
		lt, ok := byTier[tier]
		if !ok {
			t.Errorf("tier %q missing from ladder", tier)
			continue
		}
		capexOnlySavings := lt.RentalTotal - bundleCapex
		if math.Abs(capexOnlySavings-want) > 1.0 {
			t.Errorf("%s: capex-only savings = %.2f, want ~%.0f", tier, capexOnlySavings, want)
		}
	}
}

func TestOwnedElectricityIsAdded(t *testing.T) {
	l := testLadder(t)
	// 550W x8, PUE 1.15, $0.087/kWh over the window.
	wantEnergy := (avgWatts * 8 / 1000.0) * 1.15 * windowHours * 0.087
	if math.Abs(l.OwnedEnergyCost-wantEnergy) > 1.0 {
		t.Errorf("owned energy = %v, want ~%v", l.OwnedEnergyCost, wantEnergy)
	}
	if math.Abs(l.OwnedFullyLoaded-(bundleCapex+wantEnergy)) > 1.0 {
		t.Errorf("fully-loaded = %v, want ~%v", l.OwnedFullyLoaded, bundleCapex+wantEnergy)
	}
}

func TestFullyLoadedMedianSavings(t *testing.T) {
	l := testLadder(t)
	median := tierByKey(l)["dedicated_median"]
	if median.Verdict != "buy" {
		t.Errorf("median verdict = %q, want buy", median.Verdict)
	}
	// Blog's $36,657 minus ~$11,569 electricity ≈ $25,088.
	if math.Abs(median.SavingsVsBuy-25088) > 5 {
		t.Errorf("median fully-loaded savings = %v, want ~25088", median.SavingsVsBuy)
	}
}

func TestConclusionHoldsBuyWinsAtProductionTiers(t *testing.T) {
	byTier := tierByKey(testLadder(t))
	want := map[string]string{
		"spot":             "rent",
		"reserved":         "rent",
		"dedicated_median": "buy",
		"dedicated_high":   "buy",
		"premium_secure":   "buy",
	}
	for tier, verdict := range want {
		if got := byTier[tier].Verdict; got != verdict {
			t.Errorf("%s verdict = %q, want %q", tier, got, verdict)
		}
	}
}

func TestTiersSortedAscending(t *testing.T) {
	l := testLadder(t)
	for i := 1; i < len(l.Tiers); i++ {
		if l.Tiers[i].RentalHourly < l.Tiers[i-1].RentalHourly {
			t.Errorf("tiers not ascending at %d: %v < %v", i, l.Tiers[i].RentalHourly, l.Tiers[i-1].RentalHourly)
		}
	}
}

// Fix #1: a run that uses fewer GPUs than the bundle (e.g. the default compose's
// TP1xPP4 = 4 GPUs against the 8-GPU bundle) must price the GPUs actually used —
// the bundle contributes a per-GPU rate, not the whole-node count.
func TestSubNodeBundleScalesByGPUsUsed(t *testing.T) {
	e := testEngine(t)
	l := e.RentVsBuyLadder("RTX 5090", 4, windowHours, avgWatts, InfraConfig{SystemBundle: bundle})
	if l.EffectiveNumGPUs != 4 {
		t.Errorf("effective GPUs = %d, want 4 (must not inherit the bundle's 8)", l.EffectiveNumGPUs)
	}
	// 4 of 8 GPUs => half the all-in node CapEx.
	wantCapex := bundleCapex / 2.0
	if math.Abs(l.OwnedCapexOnly-wantCapex) > 0.01 {
		t.Errorf("owned capex = %v, want %v", l.OwnedCapexOnly, wantCapex)
	}
	// Electricity should scale to 4 GPUs, not 8.
	wantEnergy := (avgWatts * 4 / 1000.0) * 1.15 * windowHours * 0.087
	if math.Abs(l.OwnedEnergyCost-wantEnergy) > 1.0 {
		t.Errorf("owned energy = %v, want ~%v", l.OwnedEnergyCost, wantEnergy)
	}
}

func TestCalculateLLMTokenCosts(t *testing.T) {
	e := testEngine(t)
	res := e.CalculateLLM(LLMInput{
		GPUName:           "NVIDIA GeForce RTX 5090",
		NumGPUs:           8,
		DurationSeconds:   windowHours * 3600,
		AvgPowerWatts:     avgWatts,
		TotalInputTokens:  1_000_000,
		TotalOutputTokens: 1_000_000,
		Infra:             InfraConfig{SystemBundle: bundle},
	})
	if res.RentVsBuy.EffectiveNumGPUs != 8 {
		t.Errorf("effective GPUs = %d, want 8", res.RentVsBuy.EffectiveNumGPUs)
	}
	// Output tokens are weighted 3x input.
	if res.CorespanInfra.Output <= res.CorespanInfra.Input {
		t.Errorf("output cost %.4f should exceed input cost %.4f (3:1 weighting)", res.CorespanInfra.Output, res.CorespanInfra.Input)
	}
	if res.CorespanInfra.Blended <= 0 {
		t.Errorf("blended cost should be positive, got %v", res.CorespanInfra.Blended)
	}
}
