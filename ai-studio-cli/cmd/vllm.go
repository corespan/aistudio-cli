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
	RunE: func(cmd *cobra.Command, args []string) error {
		argsList := composeArgs("up", "-d")

		fmt.Printf("Executing: docker %s\n", strings.Join(argsList, " "))
		c := exec.Command("docker", argsList...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			return fmt.Errorf("failed to start vLLM compose stack: %w", err)
		}

		fmt.Printf("Waiting for vLLM endpoint %s to become ready (timeout %ds)...\n", vllmEndpoint, upTimeoutSec)
		if err := waitForVLLMReady(vllmEndpoint, time.Duration(upTimeoutSec)*time.Second); err != nil {
			return err
		}

		fmt.Println("vLLM is ready and serving requests!")
		return nil
	},
}

var vllmDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop and remove the vLLM compose stack",
	RunE: func(cmd *cobra.Command, args []string) error {
		argsList := composeArgs("down")
		fmt.Printf("Executing: docker %s\n", strings.Join(argsList, " "))
		c := exec.Command("docker", argsList...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

var vllmStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show status of the vLLM service and currently loaded models",
	RunE: func(cmd *cobra.Command, args []string) error {
		c := exec.Command("docker", composeArgs("ps")...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		_ = c.Run()

		fmt.Println("\nChecking API status...")
		models, err := getVLLMModels(vllmEndpoint)
		if err != nil {
			fmt.Printf("vLLM Endpoint (%s) is offline or loading: %v\n", vllmEndpoint, err)
		} else {
			fmt.Printf("vLLM Endpoint (%s) is ONLINE\n", vllmEndpoint)
			fmt.Println("Loaded Models:")
			for _, m := range models {
				fmt.Printf(" - %s\n", m)
			}
		}
		return nil
	},
}

var vllmLogsCmd = &cobra.Command{
	Use:   "logs [service]",
	Short: "Show logs from the vLLM container",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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

		argsList := composeArgs(extra...)
		c := exec.Command("docker", argsList...)
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}

func init() {
	vllmCmd.PersistentFlags().StringVar(&composeFile, "compose-file", "", "Path to docker-compose.yml file")
	vllmCmd.PersistentFlags().StringVar(&vllmEndpoint, "endpoint", "http://localhost:8010", "vLLM service API base URL")

	vllmUpCmd.Flags().IntVar(&upTimeoutSec, "timeout", 300, "Maximum time in seconds to wait for model initialisation")
	vllmLogsCmd.Flags().StringVar(&logTail, "tail", "all", "Number of lines to show from the end of the logs")
	vllmLogsCmd.Flags().BoolVarP(&logFollow, "follow", "f", false, "Follow log output")

	vllmCmd.AddCommand(vllmUpCmd)
	vllmCmd.AddCommand(vllmDownCmd)
	vllmCmd.AddCommand(vllmStatusCmd)
	vllmCmd.AddCommand(vllmLogsCmd)
	rootCmd.AddCommand(vllmCmd)
}

func composeArgs(args ...string) []string {
	base := []string{"compose"}
	if composeFile != "" {
		base = append(base, "-f", composeFile)
	}
	return append(base, args...)
}

func waitForVLLMReady(endpoint string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	url := fmt.Sprintf("%s/v1/models", strings.TrimSuffix(endpoint, "/"))
	client := &http.Client{Timeout: 5 * time.Second}

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for vLLM models API to become ready at %s", endpoint)
		case <-ticker.C:
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