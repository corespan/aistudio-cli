package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".ai-studio-cli"
	}
	return filepath.Join(home, ".ai-studio-cli")
}

func ConfigFilePath() string {
	return filepath.Join(configDir(), "config.yaml")
}

func Init() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(configDir())
	viper.AddConfigPath(".")

	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			fmt.Fprintf(os.Stderr, "Warning: config file error: %v\n", err)
		}
	}
}

// BindFlags binds any persistent root-level flags to viper keys.
// Nothing to bind at this time — kept for future extensibility.
func BindFlags(cmd *cobra.Command) {}

func GetSetupSudoPassword() string { return viper.GetString("setup.sudo_password") }

func Save() error {
	dir := configDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("cannot create config directory %s: %w", dir, err)
	}
	return viper.WriteConfigAs(ConfigFilePath())
}
