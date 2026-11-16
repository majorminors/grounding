package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// An error a command returns is printed once, by Execute, without the
// usage text; a flag Cobra cannot parse still gets the usage.
func TestCommandErrorsOmitUsageButFlagErrorsKeepIt(t *testing.T) {
	cases := []struct {
		name           string
		arguments      []string
		wantUsageShown bool
	}{
		{"an error the command returns", []string{"download"}, false},
		{"a flag that does not exist", []string{"download", "--no-such-flag"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var printed bytes.Buffer
			rootCmd.SetOut(&printed)
			rootCmd.SetErr(&printed)
			rootCmd.SetArgs(c.arguments)
			defer func() {
				rootCmd.SetOut(nil)
				rootCmd.SetErr(nil)
				rootCmd.SetArgs(nil)
				findDownloadLinkCmd.SilenceUsage = false
			}()

			if err := rootCmd.Execute(); err == nil {
				t.Fatal("want an error")
			}
			if shown := strings.Contains(printed.String(), "Usage:"); shown != c.wantUsageShown {
				t.Errorf("usage shown = %v, want %v; printed:\n%s", shown, c.wantUsageShown, printed.String())
			}
			if strings.Contains(printed.String(), "Error:") {
				t.Errorf("Cobra printed the error itself; Execute should be the only one:\n%s", printed.String())
			}
		})
	}
}
