package cmd

import (
	"fmt"
)

func serverLifecycle(composeFile, endpoint string, timeoutSec int, keepServer bool) (string, func(), error) {
	effectiveCompose := composeFile
	var composeCleanup func()
	startedServer := false

	if effectiveCompose == "" {
		if !isVLLMReachable(endpoint) {
			fmt.Println("No --compose-file provided and server is unreachable.")
			fmt.Println("Starting vLLM using bundled default docker-compose.yaml...")
			path, cleanup, err := generateComposeFile()
			if err != nil {
				return "", nil, fmt.Errorf("extracting default compose file: %w", err)
			}
			composeCleanup = cleanup
			effectiveCompose = path
		}
	}

	if effectiveCompose != "" {
		if !isVLLMReachable(endpoint) {
			if err := composeUp(effectiveCompose, endpoint, timeoutSec); err != nil {
				fmt.Println("\n--- Tearing down failed vLLM stack ---")
				_ = composeDown(effectiveCompose)
				if composeCleanup != nil {
					composeCleanup()
				}
				return "", nil, err
			}
			startedServer = true
		} else {
			fmt.Printf("vLLM endpoint %s is already reachable — skipping compose up.\n", endpoint)
		}
	}

	// 3. Return the teardown function.
	teardown := func() {
		// Only tear down if we started the server and is not kept by the user
		if startedServer && !keepServer {
			fmt.Println("\n--- Tearing down vLLM server ---")
			if err := composeDown(effectiveCompose); err != nil {
				fmt.Printf("Warning: compose down failed: %v\n", err)
			}
		}
		if composeCleanup != nil {
			composeCleanup()
		}
	}

	return effectiveCompose, teardown, nil
}
