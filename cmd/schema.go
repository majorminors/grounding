package cmd

import (
	"encoding/json"
	"os"
	"sort"

	"github.com/spf13/cobra"

	"btr.mt/grounding/internal/tools"
)

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Output MCP tool definitions as JSON",
	Long:  `Outputs all registered MCP tool definitions as JSON, useful for documentation or integration.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		allTools := tools.All()
		sort.Slice(allTools, func(i, j int) bool {
			return allTools[i].Name < allTools[j].Name
		})

		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(allTools)
	},
}

func init() {
	rootCmd.AddCommand(schemaCmd)
}
