package cmd

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed assets/docker-compose.yaml
var defaultComposeFile []byte

// generateComposeFile extracts the bundled docker-compose.yaml to a stable config directory.
func generateComposeFile() (path string, cleanup func(), err error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	dir := filepath.Join(configDir, "ai-studio-cli")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", nil, fmt.Errorf("creating config dir: %w", err)
	}

	path = filepath.Join(dir, "docker-compose.yaml")

	needWrite := true
	if existing, readErr := os.ReadFile(path); readErr == nil {
		needWrite = !bytes.Equal(existing, defaultComposeFile)
	}
	if needWrite {
		if err := os.WriteFile(path, defaultComposeFile, 0644); err != nil {
			return "", nil, fmt.Errorf("writing compose file: %w", err)
		}
	}

	return path, func() {}, nil
}
