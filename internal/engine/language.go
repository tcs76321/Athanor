package engine

import (
	"context"
	"strings"

	"github.com/tcs76321/athanor/internal/job"
	"github.com/tcs76321/athanor/internal/project"
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
func (e *Engine) languageFor(ctx context.Context, j job.Job, p project.Project, t project.Task) string {
	if l := e.state.Language(ctx, j.ID); project.ValidLanguage(l) {
		return l
	}
	if l := project.DetectLanguage(p.RepositoryPath, p.TestCommand()); l != project.LanguageUnknown {
		return l
	}
	return project.LanguageUnknown
}

// codeInstruction is the language-aware generation instruction. The Python
// wording is the historical one; other languages only change the source name
// and the "no fences" rule. The multi-file marker convention is shared.
func codeInstruction(language string) string {
	noun := "source code"
	switch language {
	case project.LanguagePython:
		noun = "Python source"
	case project.LanguageGo:
		noun = "Go source"
	case project.LanguageJavaScript:
		noun = "JavaScript source"
	case project.LanguageTypeScript:
		noun = "TypeScript source"
	case project.LanguageRust:
		noun = "Rust source"
	case project.LanguageJava:
		noun = "Java source"
	case project.LanguageRuby:
		noun = "Ruby source"
	case project.LanguagePHP:
		noun = "PHP source"
	case project.LanguageShell:
		noun = "shell source"
	}
	return "Output ONLY raw " + noun + ", no markdown code fences, no prose, no commentary. " +
		"A single-file task outputs just the file contents. A multi-file task emits each file as a block beginning " +
		"with a line `=== FILE: <relative path> ===` followed by that file's raw contents; emit every file the task " +
		"requires. When editing an existing repository, preserve every existing public function, class, test, and " +
		"documentation block the task does not explicitly ask you to change; never drop unrelated code or tests."
}
