package cmd

import (
	"os"
	"testing"
)

// TestMain points the research run records, and the day logs beside
// them (page reads, catalogue searches), at a folder of the test run's
// own, so no test writes into the real state directory. A test that
// wants them elsewhere sets GROUNDING_RESEARCH_RUN_RECORDS_DIR itself.
func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "grounding-test-state-")
	if err != nil {
		panic(err)
	}
	os.Setenv("GROUNDING_RESEARCH_RUN_RECORDS_DIR", directory)
	code := m.Run()
	os.RemoveAll(directory)
	os.Exit(code)
}
