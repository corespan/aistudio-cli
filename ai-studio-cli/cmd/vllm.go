package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	composeFile  string
	vllmEndpoint string
	upTimeoutSec int
	logTail      string
	logFollow    bool
)

var vllmCmd = &cobra.Command{
	Use:   "vllm",
	Short: "Manage the vLLM Docker Compose environment",
}

var vllmUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Start the vLLM stack and wait for it to be ready",
	Long: `Starts the vLLM Docker Compose stack and waits for the model to load.

If --compose-file is not provided, the bundled default docker-compose.yaml is used.
The command polls the /v1/models endpoint until the server is ready or the timeout
is reached. If the container exits unexpectedly, the last 30 lines of logs are
printed immediately instead of waiting for the full timeout.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		effectiveCompose := composeFile
		var cleanup func()
		if effectiveCompose == "" {
			fmt.Println("No --compose-file provided; using bundled default docker-compose.yaml...")
			path, c, err := generateComposeFile()
			if err != nil {
				return fmt.Errorf("extracting default compose file: %w", err)
			}
			cleanup = c
			effectiveCompose = path
		}
		if cleanup != nil {
			defer cleanup()
		}

		if err := composeUp(effectiveCompose, vllmEndpoint, upTimeoutSec); err != nil {
			return err
		}
		fmt.Println("\n--- Status ---")
		return runVllmStatus(effectiveCompose, vllmEndpoint)
	},
}

var vllmDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop and remove the vLLM compose stack",
	RunE: func(cmd *cobra.Command, args []string) error {
		file, cleanup := resolveComposeFile()
		defer cleanup()
		return composeDown(file)
	},
}

var vllmStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of the vLLM service and currently loaded models",
	RunE: func(cmd *cobra.Command, args []string) error {
		file, cleanup := resolveComposeFile()
		defer cleanup()
		return runVllmStatus(file, vllmEndpoint)
	},
}

func runVllmStatus(file, endpoint string) error {
	c := exec.Command("docker", composeArgsForFile(file, "ps")...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	_ = c.Run()

	fmt.Println("\nChecking API status...")
	models, err := getVLLMModels(endpoint)
	if err != nil {
		fmt.Printf("vLLM Endpoint (%s) is offline or loading: %v\n", endpoint, err)
	} else {
		fmt.Printf("vLLM Endpoint (%s) is ONLINE\n", endpoint)
		fmt.Println("Loaded Models:")
		for _, m := range models {
			fmt.Printf(" - %s\n", m)
		}
	}
	return nil
}

var vllmLogsCmd = &cobra.Command{
	Use:   "logs [service]",
	Short: "Show logs from the vLLM container",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		file, cleanup := resolveComposeFile()
		defer cleanup()

		service := "vllm"
		if len(args) > 0 {
			service = args[0]
		}

		extra := []string{"logs"}
		if logFollow {
			extra = append(extra, "-f")
		}
		if logTail != "" {
			extra = append(extra, "--tail", logTail)
		}
		extra = append(extra, service)

		argsList := composeArgsForFile(file, extra...)
		c := exec.Command("docker", argsList...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

func init() {
	vllmCmd.PersistentFlags().StringVar(&composeFile, "compose-file", "", "Path to docker-compose.yml; uses bundled default when omitted")
	vllmCmd.PersistentFlags().StringVar(&vllmEndpoint, "endpoint", "http://localhost:8010", "vLLM service API base URL")

	// 900s (15 min) default - It takes time to load model weights.
	vllmUpCmd.Flags().IntVar(&upTimeoutSec, "timeout", 900, "Maximum seconds to wait for model initialisation")
	vllmLogsCmd.Flags().StringVar(&logTail, "tail", "all", "Number of lines to show from the end of the logs")
	vllmLogsCmd.Flags().BoolVarP(&logFollow, "follow", "f", false, "Follow log output")

	vllmCmd.AddCommand(vllmUpCmd)
	vllmCmd.AddCommand(vllmDownCmd)
	vllmCmd.AddCommand(vllmStatusCmd)
	vllmCmd.AddCommand(vllmLogsCmd)
	rootCmd.AddCommand(vllmCmd)
}


// resolveComposeFile returns the configured --compose-file, or extracts the
// bundled default. The returned cleanup is always safe to defer.
func resolveComposeFile() (string, func()) {
	if composeFile != "" {
		return composeFile, func() {}
	}
	if path, cleanup, err := generateComposeFile(); err == nil {
		return path, cleanup
	}
	return "", func() {}
}

// composeArgsForFile builds docker compose arguments using an explicit compose file.
func composeArgsForFile(file string, args ...string) []string {
	base := []string{"compose"}
	if file != "" {
		base = append(base, "-f", file)
	}
	return append(base, args...)
}

// composeUp starts the compose stack and waits for the vLLM endpoint to become ready.
func composeUp(file, endpoint string, timeoutSec int) error {
	argsList := composeArgsForFile(file, "up", "-d")

	fmt.Printf("Executing: docker %s\n", strings.Join(argsList, " "))
	c := exec.Command("docker", argsList...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return fmt.Errorf("failed to start vLLM compose stack: %w", err)
	}

	fmt.Printf("Waiting for vLLM endpoint %s to become ready (timeout %ds)...\n", endpoint, timeoutSec)
	if err := waitForVLLMReady(endpoint, file, time.Duration(timeoutSec)*time.Second); err != nil {
		return err
	}

	fmt.Println("vLLM is ready and serving requests!")
	return nil
}

// composeDown tears down the compose stack.
func composeDown(file string) error {
	argsList := composeArgsForFile(file, "down")
	fmt.Printf("Executing: docker %s\n", strings.Join(argsList, " "))
	c := exec.Command("docker", argsList...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// isVLLMReachable checks if the vLLM endpoint is already serving models.
func isVLLMReachable(endpoint string) bool {
	_, err := getVLLMModelsWithTimeout(endpoint, 3*time.Second)
	return err == nil
}

// waitForVLLMReady polls the /v1/models endpoint until it returns 200 OK
// or failure.
func waitForVLLMReady(endpoint, composeFile string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	url := fmt.Sprintf("%s/v1/models", strings.TrimSuffix(endpoint, "/"))
	client := &http.Client{Timeout: 5 * time.Second}

	for {
		select {
		case <-ctx.Done():
			// Timeout — print logs to help diagnose the failure
			fmt.Println()
			printContainerLogs(composeFile, 50)
			return fmt.Errorf(
				"timed out after %s waiting for vLLM at %s\n  Run 'ai-studio-cli vllm logs' for full output",
				timeout, endpoint,
			)

		case <-ticker.C:
			// Fail fast if the container itself has exited
			if err := checkContainerStillRunning(composeFile); err != nil {
				fmt.Println()
				return err
			}

			req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
			if err != nil {
				continue
			}

			resp, err := client.Do(req)
			if err != nil {
				fmt.Print(".")
				continue
			}

			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				fmt.Println()
				return nil
			}
			fmt.Print(".")
		}
	}
}

// checkContainerStillRunning returns an error
func checkContainerStillRunning(composeFile string) error {
	args := composeArgsForFile(composeFile, "ps", "--status", "exited", "--status", "restarting", "--quiet")
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		// Can't determine status — keep waiting rather than failing
		return nil
	}
	if strings.TrimSpace(string(out)) == "" {
		// No exited containers
		return nil
	}

	// At least one container has exited — grab logs and surface them
	logs := captureContainerLogs(composeFile, 30)
	return fmt.Errorf(
		"vLLM container exited unexpectedly.\n\nLast logs:\n%s\n"+
			"  Hint: check GPU memory, model name, or run 'ai-studio-cli vllm logs' for full output",
		logs,
	)
}

// printContainerLogs prints the last n lines of compose logs to stdout.
func printContainerLogs(composeFile string, lines int) {
	fmt.Printf("\n--- Last %d log lines ---\n", lines)
	fmt.Print(captureContainerLogs(composeFile, lines))
	fmt.Println("---")
}

// captureContainerLogs returns the last n log lines from the compose stack as a string.
func captureContainerLogs(composeFile string, lines int) string {
	args := composeArgsForFile(composeFile, "logs", "--tail", fmt.Sprintf("%d", lines))
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		return fmt.Sprintf("(could not retrieve logs: %v)\n", err)
	}
	return string(out)
}

type openaiModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

func getVLLMModels(endpoint string) ([]string, error) {
	return getVLLMModelsWithTimeout(endpoint, 5*time.Second)
}

func getVLLMModelsWithTimeout(endpoint string, timeout time.Duration) ([]string, error) {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	client := &http.Client{Timeout: timeout}
	url := fmt.Sprintf("%s/v1/models", strings.TrimSuffix(endpoint, "/"))

	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var oResp openaiModelsResponse
	if err := json.Unmarshal(body, &oResp); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(oResp.Data))
	for _, m := range oResp.Data {
		models = append(models, m.ID)
	}
	return models, nil
}