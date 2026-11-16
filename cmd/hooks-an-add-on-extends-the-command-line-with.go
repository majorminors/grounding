package cmd

import "github.com/spf13/cobra"

// What an add-on may add to the command line (its tools and backends
// go through internal/tools' hooks). An add-on's package imports this
// one, so core's commands exist by the time its init() runs.

// AddCommandFromAnAddOn adds a top-level command.
func AddCommandFromAnAddOn(command *cobra.Command) {
	rootCmd.AddCommand(command)
}

// CommandNamed returns core's top-level command of that name (or
// alias), for an add-on to add flags or help text to; nil when none.
func CommandNamed(name string) *cobra.Command {
	for _, command := range rootCmd.Commands() {
		if command.Name() == name || command.HasAlias(name) {
			return command
		}
	}
	return nil
}

// JSONOutputWasAsked is whether the run was given --json.
func JSONOutputWasAsked() bool {
	return jsonOutput
}
