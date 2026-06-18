package benchui

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RunEntry represents a single benchmark run discovered on disk.
type RunEntry struct {
	Model     string      `json:"model"`
	GPU       string      `json:"gpu"`
	Timestamp string      `json:"timestamp"`
	Path      string      `json:"path"`
	Summary   *RunSummary `json:"summary,omitempty"`
}

type RunSummary struct {
	Concurrency          interface{} `json:"concurrency,omitempty"`
	Completed            interface{} `json:"completed,omitempty"`
	RequestThroughput    interface{} `json:"request_throughput,omitempty"`
	OutputThroughput     interface{} `json:"output_throughput,omitempty"`
	InputThroughput      interface{} `json:"input_throughput,omitempty"`
	TotalTokenThroughput interface{} `json:"total_token_throughput,omitempty"`
	MeanTTFT             interface{} `json:"mean_ttft_ms,omitempty"`
	MeanTPOT             interface{} `json:"mean_tpot_ms,omitempty"`
	ModelConfig          interface{} `json:"model_config,omitempty"`
}

// NewRouter builds the stdlib router with API and static file routes.
func NewRouter(resultDir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/runs", listRunsHandler(resultDir))
	mux.HandleFunc("GET /api/v1/runs/{model}/{gpu}/{timestamp}", getRunHandler(resultDir))
	mux.HandleFunc("GET /", spaHandler())
	return corsMiddleware(recoverer(mux))
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Fprintf(os.Stderr, "panic serving %s: %v\n", r.URL.Path, rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func listRunsHandler(resultDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		runs, err := discoverRuns(resultDir)
		if err != nil {
			http.Error(w, fmt.Sprintf("error scanning results: %v", err), http.StatusInternalServerError)
			return
		}
		writeJSON(w, runs)
	}
}

func getRunHandler(resultDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		model := r.PathValue("model")
		gpu := r.PathValue("gpu")
		timestamp := r.PathValue("timestamp")

		dir := filepath.Join(resultDir, model, gpu, timestamp)

		// Prevent path traversal
		cleanBase := filepath.Clean(resultDir) + string(os.PathSeparator)
		cleanPath := filepath.Clean(dir) + string(os.PathSeparator)
		if !strings.HasPrefix(cleanPath, cleanBase) {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}
		resultFile := findResultFile(dir)
		if resultFile == "" {
			http.Error(w, "benchmark result not found", http.StatusNotFound)
			return
		}
		data, err := os.ReadFile(resultFile)
		if err != nil {
			http.Error(w, fmt.Sprintf("error reading result: %v", err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}
}

func spaHandler() http.HandlerFunc {
	sub, _ := fs.Sub(staticFS, "ui")
	fileServer := http.FileServer(http.FS(sub))
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(sub, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}
}

func discoverRuns(resultDir string) ([]RunEntry, error) {
	var runs []RunEntry
	if _, err := os.Stat(resultDir); os.IsNotExist(err) {
		return runs, nil
	}
	models, err := os.ReadDir(resultDir)
	if err != nil {
		return nil, fmt.Errorf("reading result dir: %w", err)
	}
	for _, modelEntry := range models {
		if !modelEntry.IsDir() {
			continue
		}
		modelPath := filepath.Join(resultDir, modelEntry.Name())
		gpus, err := os.ReadDir(modelPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to read model directory %s: %v\n", modelPath, err)
			continue
		}
		for _, gpuEntry := range gpus {
			if !gpuEntry.IsDir() {
				continue
			}
			gpuPath := filepath.Join(modelPath, gpuEntry.Name())
			timestamps, err := os.ReadDir(gpuPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to read GPU directory %s: %v\n", gpuPath, err)
				continue
			}
			for _, tsEntry := range timestamps {
				if !tsEntry.IsDir() {
					continue
				}
				tsPath := filepath.Join(gpuPath, tsEntry.Name())
				resultFile := findResultFile(tsPath)
				if resultFile == "" {
					continue
				}
				entry := RunEntry{
					Model:     modelEntry.Name(),
					GPU:       gpuEntry.Name(),
					Timestamp: tsEntry.Name(),
					Path:      tsPath,
					Summary:   peekSummary(resultFile),
				}
				runs = append(runs, entry)
			}
		}
	}
	return runs, nil
}

func findResultFile(dir string) string {
	for _, name := range []string{"benchmark_result.json", "result.json"} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

type summaryCacheEntry struct {
	modTime time.Time
	size    int64
	summary *RunSummary
}

var (
	summaryCache   = make(map[string]summaryCacheEntry)
	summaryCacheMu sync.Mutex
)

// rawSummary mirrors only the fields we surface.
type rawSummary struct {
	MaxConcurrency       interface{} `json:"max_concurrency"`
	Completed            interface{} `json:"completed"`
	RequestThroughput    interface{} `json:"request_throughput"`
	OutputThroughput     interface{} `json:"output_throughput"`
	InputThroughput      interface{} `json:"input_throughput"`
	TotalTokenThroughput interface{} `json:"total_token_throughput"`
	MeanTTFT             interface{} `json:"mean_ttft_ms"`
	MeanTPOT             interface{} `json:"mean_tpot_ms"`
	ModelConfig          interface{} `json:"model_config"`
}

func peekSummary(path string) *RunSummary {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}

	summaryCacheMu.Lock()
	if e, ok := summaryCache[path]; ok && e.modTime.Equal(info.ModTime()) && e.size == info.Size() {
		summaryCacheMu.Unlock()
		return e.summary
	}
	summaryCacheMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var raw rawSummary
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return nil
	}

	s := &RunSummary{
		Concurrency:          raw.MaxConcurrency,
		Completed:            raw.Completed,
		RequestThroughput:    raw.RequestThroughput,
		OutputThroughput:     raw.OutputThroughput,
		InputThroughput:      raw.InputThroughput,
		TotalTokenThroughput: raw.TotalTokenThroughput,
		MeanTTFT:             raw.MeanTTFT,
		MeanTPOT:             raw.MeanTPOT,
		ModelConfig:          raw.ModelConfig,
	}

	summaryCacheMu.Lock()
	summaryCache[path] = summaryCacheEntry{modTime: info.ModTime(), size: info.Size(), summary: s}
	summaryCacheMu.Unlock()

	return s
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(out)
}
