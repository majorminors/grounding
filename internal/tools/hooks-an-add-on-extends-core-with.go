package tools

import "context"

// What an add-on may add to core beyond its backends (the registry in
// registry-of-search-sources-citation-verifiers-and-health-probes.go)
// and its download providers (find-download-link.go): text in a tool's
// full-tier description, parameters on a tool, sources listed on the
// full-tier landing page, and environment variables a research run's
// child grounding needs. All of it is called from an add-on's init(),
// after core's tools are registered (an add-on imports core's cmd).
// Core itself never names an add-on's backend (PLAN "Core and add-on
// split").

// AppendToFullTierDescription adds a paragraph to the description a
// full-tier caller sees of a registered tool. It reports false when no
// tool has that name.
func AppendToFullTierDescription(toolName, paragraph string) bool {
	for i := range registry {
		if registry[i].Name != toolName {
			continue
		}
		description := registry[i].FullTierDescription
		if description == "" {
			description = registry[i].Description
		}
		registry[i].FullTierDescription = description + "\n\n" + paragraph
		return true
	}
	return false
}

// AddParameterToTool adds a parameter to a registered tool's input
// schema, with its command-line flag (the add-on defines the flag on
// the command itself). It reports false when no tool has that name.
func AddParameterToTool(toolName, parameter string, property Property, commandLineFlag string) bool {
	for i := range registry {
		if registry[i].Name != toolName {
			continue
		}
		properties := make(map[string]Property, len(registry[i].InputSchema.Properties)+1)
		for name, existing := range registry[i].InputSchema.Properties {
			properties[name] = existing
		}
		properties[parameter] = property
		registry[i].InputSchema.Properties = properties
		if commandLineFlag != "" {
			flags := make(map[string]string, len(registry[i].CLI.Flags)+1)
			for name, flag := range registry[i].CLI.Flags {
				flags[name] = flag
			}
			flags[parameter] = commandLineFlag
			registry[i].CLI.Flags = flags
		}
		return true
	}
	return false
}

// SourceOnTheLandingPage is one line of the landing page's sources.
type SourceOnTheLandingPage struct {
	Name, What string
}

var sourcesOnTheFullTierLandingPageAddedByAddOns []SourceOnTheLandingPage

// ListOnTheFullTierLandingPage shows a source to authenticated
// visitors of the landing page, after core's.
func ListOnTheFullTierLandingPage(source SourceOnTheLandingPage) {
	sourcesOnTheFullTierLandingPageAddedByAddOns = append(sourcesOnTheFullTierLandingPageAddedByAddOns, source)
}

// SourcesOnTheFullTierLandingPageAddedByAddOns is what add-ons listed.
func SourcesOnTheFullTierLandingPageAddedByAddOns() []SourceOnTheLandingPage {
	return sourcesOnTheFullTierLandingPageAddedByAddOns
}

var environmentVariablesResearchRunsPassOnAddedByAddOns []string

// PassEnvironmentVariablesToResearchRuns names variables a research
// run's child grounding needs to reach an add-on's backends.
func PassEnvironmentVariablesToResearchRuns(names ...string) {
	environmentVariablesResearchRunsPassOnAddedByAddOns = append(environmentVariablesResearchRunsPassOnAddedByAddOns, names...)
}

type commandLineRunContextKey struct{}

// WithCommandLineRun marks a context as a command-line run's, in which
// a backend may fetch its key by running a command (config
// SecretFromEnvironmentOrItsCommand); the server never does.
func WithCommandLineRun(ctx context.Context) context.Context {
	return context.WithValue(ctx, commandLineRunContextKey{}, true)
}

// IsCommandLineRun is whether the context is a command-line run's.
func IsCommandLineRun(ctx context.Context) bool {
	run, _ := ctx.Value(commandLineRunContextKey{}).(bool)
	return run
}
