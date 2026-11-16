package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"btr.mt/grounding/internal/config"
	"btr.mt/grounding/internal/tools"
)

// research-runs reads the records research_question keeps of its runs
// (internal/tools/research_run_record.go): list them, show one, or ask
// one's question again. It reads the directory the server writes to,
// so on the server it must run as the server's user with the server's
// GROUNDING_RESEARCH_RUN_RECORDS_DIR.

var (
	researchRunsDirectory    string
	researchRunsShowFile     string
	researchRunsReplayModel  string
	researchRunsReplayEffort string
)

var researchRunsCmd = &cobra.Command{
	Use:   "research-runs",
	Short: "List, show and replay the research_question runs this server has kept",
	Long: `research_question keeps a record of every run: the question, who asked, the
model and effort, timings, turns, cost and outcome, Claude Code's whole stream
(or the whole API conversation), the ledger of what the researcher was shown,
its backbrief, and what the caller was handed. Records hold questions and whole
pages, so they are readable by the server's user alone.

Examples:
  grounding research-runs list
  grounding research-runs show research-3f9a
  grounding research-runs show research-3f9a --file claude-code-stream.jsonl
  grounding research-runs replay research-3f9a --effort high`,
}

var researchRunsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the kept runs, oldest first",
	Args:  cobra.NoArgs,
	RunE:  runResearchRunsList,
}

var researchRunsShowCmd = &cobra.Command{
	Use:   "show RUN",
	Short: "Show a run's record (RUN is its session ID, its directory, or the start of either)",
	Args:  cobra.ExactArgs(1),
	RunE:  runResearchRunsShow,
}

var researchRunsReplayCmd = &cobra.Command{
	Use:   "replay RUN",
	Short: "Ask a kept run's question again, on its model and effort unless told otherwise",
	Args:  cobra.ExactArgs(1),
	RunE:  runResearchRunsReplay,
}

func init() {
	researchRunsCmd.PersistentFlags().StringVar(&researchRunsDirectory, "dir", "",
		"the records' directory (default: GROUNDING_RESEARCH_RUN_RECORDS_DIR, else the server's default)")
	researchRunsShowCmd.Flags().StringVar(&researchRunsShowFile, "file", "",
		"print this file of the run instead of its summary: "+strings.Join(tools.ResearchRunArtefacts, ", "))
	researchRunsReplayCmd.Flags().StringVar(&researchRunsReplayModel, "model", "", "the researcher's model (default: the run's)")
	researchRunsReplayCmd.Flags().StringVar(&researchRunsReplayEffort, "effort", "", "how hard it thinks (default: the run's)")
	researchRunsCmd.AddCommand(researchRunsListCmd, researchRunsShowCmd, researchRunsReplayCmd)
	rootCmd.AddCommand(researchRunsCmd)
}

func researchRunsRecordsDirectory() (string, error) {
	if researchRunsDirectory != "" {
		return researchRunsDirectory, nil
	}
	if directory := tools.ResearchRunRecordsDirectory(config.LoadConfig()); directory != "" {
		return directory, nil
	}
	return "", errors.New("keeping run records is turned off here (GROUNDING_RESEARCH_RUN_RECORDS_DIR=off); name a directory with --dir")
}

func runResearchRunsList(cmd *cobra.Command, args []string) error {
	directory, err := researchRunsRecordsDirectory()
	if err != nil {
		return err
	}
	records, err := tools.ListResearchRunRecords(directory)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "No runs kept yet in %s.\n", directory)
		return nil
	}
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(records)
	}
	for _, record := range records {
		fmt.Println(researchRunSummaryLine(record))
	}
	return nil
}

// researchRunSummaryLine is one run on one line: when, which, how it
// ended, on what, who asked, and the start of the question.
func researchRunSummaryLine(record tools.ResearchRunRecord) string {
	caller := record.Caller.Through
	if record.Caller.ClientName != "" {
		caller += " " + record.Caller.ClientName
	}
	return fmt.Sprintf("%s  %s  %-19s %s/%s  %3ds  %2d turns  %d/%d supported  %s  %s",
		record.StartedAt.Local().Format("2006-01-02 15:04"), record.SessionID, record.Outcome,
		record.Model, record.Effort, int(record.DurationSeconds), record.Turns,
		record.FindingsSupported, record.Findings, caller, firstRunesOnOneLine(record.Question, 70))
}

func firstRunesOnOneLine(text string, count int) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > count {
		return string(runes[:count]) + "…"
	}
	return text
}

func runResearchRunsShow(cmd *cobra.Command, args []string) error {
	directory, err := researchRunsRecordsDirectory()
	if err != nil {
		return err
	}
	runDirectory, err := tools.FindResearchRunDirectory(directory, args[0])
	if err != nil {
		return err
	}
	if researchRunsShowFile != "" {
		if !containsName(tools.ResearchRunArtefacts, researchRunsShowFile) {
			return fmt.Errorf("a run's files are %s", strings.Join(tools.ResearchRunArtefacts, ", "))
		}
		content, err := os.ReadFile(filepath.Join(runDirectory, researchRunsShowFile))
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(content)
		return err
	}
	record, err := tools.ReadResearchRunRecord(runDirectory)
	if err != nil {
		return err
	}
	if jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(record)
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	fmt.Printf("%s\n\nIn %s:\n", encoded, runDirectory)
	for _, name := range tools.ResearchRunArtefacts {
		if info, err := os.Stat(filepath.Join(runDirectory, name)); err == nil {
			fmt.Printf("  %-28s %d bytes\n", name, info.Size())
		}
	}
	if returned, err := os.ReadFile(filepath.Join(runDirectory, tools.ResearchRunReturnedToCallerFile)); err == nil {
		fmt.Printf("\nWhat the caller was handed:\n\n%s\n", returned)
	}
	return nil
}

func containsName(names []string, wanted string) bool {
	for _, name := range names {
		if name == wanted {
			return true
		}
	}
	return false
}

// runResearchRunsReplay asks a run's question again. It is a new run,
// with its own record, spending as any run does.
func runResearchRunsReplay(cmd *cobra.Command, args []string) error {
	directory, err := researchRunsRecordsDirectory()
	if err != nil {
		return err
	}
	runDirectory, err := tools.FindResearchRunDirectory(directory, args[0])
	if err != nil {
		return err
	}
	record, err := tools.ReadResearchRunRecord(runDirectory)
	if err != nil {
		return err
	}
	model, effort := record.Model, record.Effort
	if researchRunsReplayModel != "" {
		model = researchRunsReplayModel
	}
	if researchRunsReplayEffort != "" {
		effort = researchRunsReplayEffort
	}
	fmt.Fprintf(os.Stderr, "Replaying %s (%s, %s) on %s at %s effort.\n",
		record.SessionID, record.StartedAt.Local().Format(time.DateTime), record.Outcome, model, effort)
	out, err := tools.ResearchQuestion(context.Background(), tools.ResearchQuestionOptions{
		Question: record.Question, Model: model, Effort: effort,
		Caller: tools.ResearchRunCaller{Through: "cli", ClientName: "research-runs replay of " + record.SessionID},
	})
	if err != nil {
		return err
	}
	fmt.Println(out)
	return nil
}
