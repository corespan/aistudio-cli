package cmd

import (
	"fmt"
	"os"

	"github.com/corespan/ai-studio-cli/internal/config"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ai-studio-cli",
	Short: "ai-studio-cli — GPU bandwidth benchmarking and setup",
}

func Execute() {
	config.Init()
	config.BindFlags(rootCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().String("profile", "default", "Config profile")
}
