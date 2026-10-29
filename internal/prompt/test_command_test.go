package prompt

import (
	"strings"
	"testing"

	"github.com/tcs76321/athanor/internal/llm"
)

// TestTaskContextShowsVerificationCommand pins the fairness fix: a code
// artifact is judged by the test command, so the command (including the module
// name it imports) must be in the prompt.
func TestTaskContextShowsVerificationCommand(t *testing.T) {
	const cmd = `python -c "from migrator import apply, rollback"`
	res, err := Assemble(Input{
		Phase:   llm.PhaseDiverging,
		Project: Project{Name: "migration", Archetype: "code", Goal: "implement a migration runner"},
		Task:    Task{Title: "implement the runner", TestCommand: cmd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, cmd) {
		t.Fatalf("test command missing from prompt:\n%s", res.Text)
	}
	if !strings.Contains(res.Text, "verification command") {
		t.Error("test command is not labelled as the verification edge")
	}
}
