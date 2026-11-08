package phases

import (
	"context"
	"strings"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/prompt"
)

// languageRequest asks the planner to name the source language. It is a
// constant so the parser and the prompt cannot drift.
const languageRequest = "Finally, end with one line `LANGUAGE: <name>` naming the source language of the artifact (for example python, go, javascript, typescript, rust, java, ruby, php, shell). If the task is not code, use `LANGUAGE: unknown`."

// parseLanguageHint extracts the planner's LANGUAGE: hint. The last valid
// marker wins; an absent or unrecognized value is ignored and the deterministic
// repository/test-command detection decides.
func parseLanguageHint(content string) string {
	hint := ""
	for _, line := range strings.Split(content, "\n") {
		l := strings.TrimSpace(line)
		const prefix = "LANGUAGE:"
		if !strings.HasPrefix(strings.ToUpper(l), prefix) {
			continue
		}
		v := strings.TrimSpace(l[len(prefix):])
		if n := project.NormalizeLanguage(v); n != project.LanguageUnknown {
			hint = n
		}
	}
	return hint
}

// languageFor resolves the generation language: the planner's validated hint
// first, then repository/manifest detection, then the project test command.
// Unknown is a valid answer; the prompt then stays generic instead of guessing.
func (d *Deps) languageFor(ctx context.Context, j job.Job, p project.Project, t project.Task) string {
	if l := d.State.Language(ctx, j.ID); project.ValidLanguage(l) {
		return l
	}
	if l := project.DetectLanguage(p.RepositoryPath, p.TestCommand()); l != project.LanguageUnknown {
		return l
	}
	return project.LanguageUnknown
}

// codeInstruction is the language-aware generation instruction. It lives in
// internal/prompt so the repair policy can share the exact wording.
func codeInstruction(language string) string { return prompt.CodeInstruction(language) }
