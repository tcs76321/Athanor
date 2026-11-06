// Package finetune turns Athanor's grounded, accepted work into a portable
// instruction-tuning dataset. It does not train: training and promotion are a
// separate, reversible step. The export is deliberately boring JSONL so any
// local trainer can consume it, and each row carries provenance.
package finetune

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/store"
)

// Message is one chat message in an exported example.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Example is one accepted task/artifact pair.
type Example struct {
	Messages []Message      `json:"messages"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

const systemPrompt = "You are Athanor, a careful local software and writing agent. " +
	"Follow the task and its acceptance criteria. Prefer verifiable work over confident prose."

// Export writes accepted, non-proposal artifacts as JSONL and returns the
// number of examples. It skips empty content and artifacts that are not
// usable training material.
func Export(ctx context.Context, st *store.Store, arts *artifact.Store, w io.Writer) (int, error) {
	rows, err := arts.ListAccepted(ctx, 0)
	if err != nil {
		return 0, err
	}
	enc := json.NewEncoder(w)
	n := 0
	for _, art := range rows {
		if art.Kind == artifact.KindProposal || art.Kind == artifact.KindEvaluation {
			continue
		}
		content, err := arts.ReadContent(ctx, art.ID)
		if err != nil || len(strings.TrimSpace(string(content))) == 0 {
			continue
		}
		task, criteria, project, err := taskContext(ctx, st, art)
		if err != nil {
			return n, err
		}
		if strings.TrimSpace(task) == "" {
			task = project
		}
		if strings.TrimSpace(task) == "" {
			continue
		}
		user := "Task: " + task
		if len(criteria) > 0 {
			user += "\n\nAcceptance criteria:\n- " + strings.Join(criteria, "\n- ")
		}
		ex := Example{
			Messages: []Message{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: user},
				{Role: "assistant", Content: string(content)},
			},
			Metadata: map[string]any{
				"project_id": art.ProjectID,
				"task_id":    art.TaskID,
				"artifact":   art.ID,
				"kind":       string(art.Kind),
				"version":    art.Version,
			},
		}
		if err := enc.Encode(ex); err != nil {
			return n, fmt.Errorf("writing example: %w", err)
		}
		n++
	}
	return n, nil
}

func taskContext(ctx context.Context, st *store.Store, art artifact.Artifact) (task string, criteria []string, project string, err error) {
	var title, description, criteriaJSON, projectName, projectGoal string
	row := st.DB().QueryRowContext(ctx, `
		SELECT COALESCE(t.title,''), COALESCE(t.description,''),
		       COALESCE(t.acceptance_criteria_json,'[]'),
		       p.name, p.goal
		FROM artifacts a
		JOIN projects p ON p.id = a.project_id
		LEFT JOIN tasks t ON t.id = a.task_id
		WHERE a.id = ?`, art.ID)
	if err := row.Scan(&title, &description, &criteriaJSON, &projectName, &projectGoal); err != nil {
		// The artifact exists but provenance is incomplete; use what we have.
		return "", nil, "", nil
	}
	task = strings.TrimSpace(title)
	if description = strings.TrimSpace(description); description != "" {
		task += "\n" + description
	}
	if task == "" {
		task = projectGoal
	}
	_ = json.Unmarshal([]byte(criteriaJSON), &criteria)
	return task, criteria, projectName + "\n" + projectGoal, nil
}
