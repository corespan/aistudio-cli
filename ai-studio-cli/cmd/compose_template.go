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

// generateComposeFile extracts the bundled compose file to a stable config
// directory, adapting the GPU passthrough syntax to the active container
// runtime (Docker reservation vs. podman CDI devices).
func generateComposeFile() (path string, cleanup func(), err error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	dir := filepath.Join(configDir, "ai-studio-cli")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", nil, fmt.Errorf("creating config dir: %w", err)
	}

	engine := currentEngine()
	content := composeForRuntime(defaultComposeFile, engine)

	name := "docker-compose.yaml"
	if engine.isPodman() {
		name = "podman-compose.yaml"
	}
	path = filepath.Join(dir, name)

	needWrite := true
	if existing, readErr := os.ReadFile(path); readErr == nil {
		needWrite = !bytes.Equal(existing, content)
	}
	if needWrite {
		if err := os.WriteFile(path, content, 0644); err != nil {
			return "", nil, fmt.Errorf("writing compose file: %w", err)
		}
	}

	return path, func() {}, nil
}
