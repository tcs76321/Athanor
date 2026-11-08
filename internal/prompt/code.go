package prompt

import "github.com/tcs76321/athanor/internal/project"

// CodeInstruction is the language-aware generation instruction. The Python
// wording is the historical one; other languages only change the source name
// and the "no fences" rule. The multi-file marker convention is shared.
func CodeInstruction(language string) string {
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
