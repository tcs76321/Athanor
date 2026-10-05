package project

import (
	"context"
	"errors"
	"testing"
)

// TestExecutionOverrideRoundTrips proves migration 0016 / F3-T4: a code
// project resolves to the built-in default, an override round-trips through
// SetExecution/Get, and a non-code project has no default test command.
func TestExecutionOverrideRoundTrips(t *testing.T) {
	r, _ := openRepo(t)
	ctx := context.Background()

	p, _, err := r.Create(ctx, "exec", ArchetypeCode, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.TestCommand(); got != DefaultCodeTestCommand {
		t.Errorf("default code test command = %q, want %q", got, DefaultCodeTestCommand)
	}

	ex := Execution{
		TestCommand:  "go test ./...",
		BuildCommand: "go build ./...",
		Linters:      []string{"golangci-lint"},
	}
	if err := r.SetExecution(ctx, p.ID, ex); err != nil {
		t.Fatalf("SetExecution: %v", err)
	}
	got, err := r.Get(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TestCommand() != "go test ./..." {
		t.Errorf("TestCommand() = %q, want go test ./...", got.TestCommand())
	}
	if got.Execution.BuildCommand != "go build ./..." {
		t.Errorf("BuildCommand = %q, want go build ./...", got.Execution.BuildCommand)
	}
	if len(got.Execution.Linters) != 1 || got.Execution.Linters[0] != "golangci-lint" {
		t.Errorf("Linters = %v, want [golangci-lint]", got.Execution.Linters)
	}
}

func TestTestCommandNonCodeHasNoDefault(t *testing.T) {
	r, _ := openRepo(t)
	p, _, err := r.Create(context.Background(), "doc", ArchetypeDocument, validGoal, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.TestCommand(); got != "" {
		t.Errorf("document TestCommand() = %q, want empty", got)
	}
}

func TestSetExecutionUnknownProject(t *testing.T) {
	r, _ := openRepo(t)
	if err := r.SetExecution(context.Background(), "ghost", Execution{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetExecution(ghost) = %v, want ErrNotFound", err)
	}
}
