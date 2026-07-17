// Package power samples GPU power draw via nvidia-smi during a benchmark run so
// the cost engine can charge realistic owned-side electricity. aistudio-cli has
// no Prometheus/telemetry stack, so this lightweight poller stands in for it.
package power

import (
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sampler periodically records the total power draw across all visible GPUs so
// the cost engine can bill exactly what the node consumes at the wall.
type Sampler struct {
	interval time.Duration
	stopCh   chan struct{}
	doneCh   chan struct{}

	mu    sync.Mutex
	sum   float64
	count int
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
	w, ok := queryTotalWatts()
	if !ok {
		return
	}
	s.mu.Lock()
	s.sum += w
	s.count++
	s.mu.Unlock()
}

// Stop halts sampling and returns the time-averaged TOTAL node power in watts
// (sum across all visible GPUs), or 0 if nothing could be sampled — e.g.
// nvidia-smi absent.
func (s *Sampler) Stop() float64 {
	close(s.stopCh)
	<-s.doneCh
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.count == 0 {
		return 0
	}
	return s.sum / float64(s.count)
}

// queryTotalWatts returns the summed power.draw across all visible GPUs at one
// instant (total node GPU power, busy + idle).
func queryTotalWatts() (float64, bool) {
	out, err := exec.Command("nvidia-smi", "--query-gpu=power.draw", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return 0, false
	}
	var sum float64
	var n int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		v, err := strconv.ParseFloat(line, 64)
		if err != nil {
			continue
		}
		sum += v
		n++
	}
	if n == 0 {
		return 0, false
	}
	return sum, true
}
