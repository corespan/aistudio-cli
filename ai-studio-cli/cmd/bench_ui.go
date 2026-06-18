package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/corespan/ai-studio-cli/internal/benchui"
	"github.com/spf13/cobra"
)

var (
	uiPort      int
	uiResultDir string
	uiAutoOpen  bool
)

var benchUICmd = &cobra.Command{
	Use:   "bench-ui",
	Short: "Launch a web dashboard to visualise benchmark results",
	Long: `Starts a lightweight HTTP server that serves a browser-based dashboard
for exploring vLLM benchmark results stored in the structured result directory.

The dashboard reads results from the --result-dir directory (default: bench-results/)
and presents them with interactive charts, filters, and a detailed metrics table.

The entire UI is embedded in the binary — no additional files or dependencies are needed.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runBenchUI()
	},
}

func init() {
	benchUICmd.Flags().IntVar(&uiPort, "port", 9090, "HTTP listen port for the dashboard")
	benchUICmd.Flags().StringVar(&uiResultDir, "result-dir", "bench-results", "Root of the structured benchmark results directory")
	benchUICmd.Flags().BoolVar(&uiAutoOpen, "open", false, "Automatically open the dashboard in the default browser")

	rootCmd.AddCommand(benchUICmd)
}

func runBenchUI() error {
	router := benchui.NewRouter(uiResultDir)

	addr := fmt.Sprintf(":%d", uiPort)
	localURL := fmt.Sprintf("http://localhost:%d", uiPort)
	networkURL := fmt.Sprintf("http://0.0.0.0:%d", uiPort)

	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	errCh := make(chan error, 1)

	go func() {
		fmt.Printf("\n  ┌─────────────────────────────────────────┐\n")
		fmt.Printf("  │  AI Studio Benchmark Dashboard          │\n")
		fmt.Printf("  │                                         │\n")
		fmt.Printf("  │  ➜  Local:   %-26s│\n", localURL)
		fmt.Printf("  │  ➜  Network: %-26s│\n", networkURL)
		fmt.Printf("  │  ➜  Results: %-26s│\n", uiResultDir)
		fmt.Printf("  │                                         │\n")
		fmt.Printf("  │  Press Ctrl+C to stop                   │\n")
		fmt.Printf("  └─────────────────────────────────────────┘\n\n")

		if uiAutoOpen {
			openBrowser(localURL)
		}
	}()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-stop:
		fmt.Println("\nShutting down...")
	case err := <-errCh:
		return fmt.Errorf("server failed: %w (is port %d already in use?)", err, uiPort)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "could not open browser: %v\n", err)
	}
}
