package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"btr.mt/grounding/internal/backends"
)

var (
	version    = "dev"
	jsonOutput bool
)

var rootCmd = &cobra.Command{
	Use:   "grounding",
	Short: "Grounding tools for AI reasoning",
	Long:  `MCP server providing grounding tools—connecting claims to verifiable sources.`,
	// Execute prints the error once; Cobra's own "Error:" line would
	// repeat it.
	SilenceErrors: true,
	// Flags and arguments have parsed by the time this runs, so a
	// mistyped flag still gets the usage text and an error the
	// command itself returns (a missing key, a failed lookup) is one
	// line. A subcommand with a PersistentPreRun of its own would
	// replace this one.
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		cmd.SilenceUsage = true
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(version)
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output in JSON format")
	rootCmd.AddCommand(versionCmd)
	// Stamp the build version into the User-Agent so upstreams can
	// tell releases apart. Set at build time with
	//   go build -ldflags "-X btr.mt/grounding/cmd.version=$(git describe --tags --always)"
	backends.Version = version
}
