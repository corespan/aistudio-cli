package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/corespan/ai-studio-cli/internal/notices"
	"github.com/spf13/cobra"
)

// A Go binary statically links every dependency. When we publish a release
// binary we are distributing compiled copies of cobra, viper, pflag and the
// rest — and MIT, BSD-3 and Apache-2.0 all condition redistribution on carrying
// the copyright notice. Unlike a Python or Node project, there is no
// site-packages or node_modules alongside the artifact for those notices to
// live in: the binary is the entire distribution.
//
// So the notices are compiled in too, and this command prints them. It is the
// same approach kubectl, docker and gh take, and it is the only one that
// survives someone copying the binary onto a machine with no network and no
// repository checkout.
var licensesCmd = &cobra.Command{
	Use:   "licenses",
	Short: "Print third-party licence notices for the software in this binary",
	Long: `Print the licences of the open-source software compiled into this binary.

A Go binary statically links its dependencies, so distributing ai-studio-cli
means distributing copies of that code. The notices below accompany it, as
those licences require.

The embedded web UI's fonts and Chart.js are covered too — they are compiled in
via go:embed.

CoreSpan AI's own source is Apache-2.0 and is not covered by these notices.
See https://github.com/corespan/aistudio-cli for the LICENSE and NOTICE files.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		text := notices.Text()

		if strings.TrimSpace(text) == "" || notices.IsPlaceholder() {
			// Better to say so than to print an empty page and let the reader
			// conclude this software has no third-party dependencies.
			//
			// Two different situations produce this, and the message has to
			// serve both: a developer's own `make build` (expected, harmless)
			// and a released binary (a packaging fault that must not happen,
			// and which the release workflow is set up to prevent).
			fmt.Fprintln(os.Stderr,
				"This binary was built without its third-party licence notices.")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr,
				"If you built it yourself, that is expected — the notices are")
			fmt.Fprintln(os.Stderr,
				"generated rather than committed. Build with them included:")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "    make build-release")
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr,
				"If this is a released binary, it is a packaging fault. Please report it:")
			fmt.Fprintln(os.Stderr,
				"https://github.com/corespan/aistudio-cli/issues")
			return fmt.Errorf("licence notices not embedded in this build")
		}

		// `licenses <module>` filters to one dependency. Handy when someone
		// only needs to check a single package's terms.
		if len(args) == 1 {
			needle := strings.ToLower(args[0])
			var matched []string
			for _, block := range strings.Split(text, "\n"+strings.Repeat("-", 72)+"\n") {
				if strings.Contains(strings.ToLower(block), needle) {
					matched = append(matched, strings.TrimSpace(block))
				}
			}
			if len(matched) == 0 {
				return fmt.Errorf("no dependency matching %q is compiled into this binary", args[0])
			}
			fmt.Println(strings.Join(matched, "\n\n"+strings.Repeat("-", 72)+"\n\n"))
			return nil
		}

		fmt.Println(text)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(licensesCmd)
}
