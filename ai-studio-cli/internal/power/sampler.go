// Package power samples GPU power draw via nvidia-smi during a benchmark run so
// the cost engine can charge realistic owned-side electricity. aistudio-cli has
// no Prometheus/telemetry stack, so this lightweight poller stands in for it.
//
// It records per-GPU draw so the caller can bill only the GPUs actually in use
// (see cost integration), keeping electricity consistent with the per-GPU CapEx.
package power

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sampler periodically records per-GPU power draw across visible GPUs.
type Sampler struct {
	interval time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}

	mu      sync.Mutex
	sums    []float64 // running sum of watts per GPU index
	counts  []int     // samples counted per GPU index
}

// NewSampler returns a sampler that polls every interval (default 2s).
func NewSampler(interval time.Duration) *Sampler {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return &Sampler{
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start begins sampling in the background. Call Stop exactly once afterwards.
func (s *Sampler) Start() {
	go func() {
		defer close(s.doneCh)
		s.sampleOnce() // immediate first sample
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				s.sampleOnce()
			}
		}
	}()
}

func (s *Sampler) sampleOnce() {
	watts, ok := queryPerGPUWatts()
	if !ok {
		return
	}
	s.mu.Lock()
	if s.sums == nil {
		s.sums = make([]float64, len(watts))
		s.counts = make([]int, len(watts))
	}
	for i, w := range watts {
		if i < len(s.sums) {
			s.sums[i] += w
			s.counts[i]++
		}
	}
	s.mu.Unlock()
}

// Stop halts sampling and returns the time-averaged watts for each visible GPU
// (one entry per GPU index). Empty if nothing could be sampled — e.g.
// nvidia-smi absent. The caller decides which GPUs count as "in use".
func (s *Sampler) Stop() []float64 {
	close(s.stopCh)
	<-s.doneCh
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]float64, 0, len(s.sums))
	for i := range s.sums {
		if s.counts[i] > 0 {
			out = append(out, s.sums[i]/float64(s.counts[i]))
		}
	}
	return out
}

// queryPerGPUWatts returns one power.draw reading per visible GPU at one instant.
func queryPerGPUWatts() ([]float64, bool) {
	out, err := exec.Command("nvidia-smi", "--query-gpu=power.draw", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, false
	}
	var watts []float64
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		v, err := strconv.ParseFloat(line, 64)
		if err != nil {
			continue
		}
		watts = append(watts, v)
	}
	if len(watts) == 0 {
		return nil, false
	}
	return watts, true
}
