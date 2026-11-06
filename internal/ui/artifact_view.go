package ui

import (
	"html/template"
	"sort"
	"strings"

	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/project"
	"github.com/tcs76321/athanor/internal/toolenvelope"
)

// artifactFile is one readable file in an artifact. Code artifacts may carry a
// multi-file tree (ADR-0065); everything else is a single file. HTML/Gutter are
// the presentation form; Content stays the copy/download source.
type artifactFile struct {
	Path    string
	Content string
	HTML    template.HTML
	Gutter  string
	Lang    string
}

// decorateFiles adds highlighted HTML, a line-number gutter, and a language
// label. It is a no-op for empty content.
func decorateFiles(files []artifactFile) []artifactFile {
	for i := range files {
		files[i].HTML = highlightCode(files[i].Path, files[i].Content)
		files[i].Gutter = lineNumbers(files[i].Content)
		files[i].Lang = highlightLanguageLabel(files[i].Path)
	}
	return files
}

// artifactMode is the presentation contract for an artifact's bytes.
type artifactMode string

const (
	modeProse   artifactMode = "prose"
	modeCode    artifactMode = "code"
	modeDataset artifactMode = "dataset"
	modeConfig  artifactMode = "config"
	modeMedia   artifactMode = "media"
)

// artifactModeFor chooses the reader. Kind is the stored artifact type;
// capability is the task's primary output kind and is used only to
// disambiguate the generic `proposal` kind (a code candidate must not be
// rendered as Markdown).
func artifactModeFor(kind, capability string) artifactMode {
	switch kind {
	case string(artifact.KindCode):
		return modeCode
	case string(artifact.KindDataset):
		return modeDataset
	case string(artifact.KindConfiguration):
		return modeConfig
	case string(artifact.KindMedia):
		return modeMedia
	case string(artifact.KindProposal):
		switch capability {
		case project.CapabilityCode:
			return modeCode
		case project.CapabilityData:
			return modeDataset
		default:
			return modeProse
		}
	default:
		return modeProse
	}
}

// artifactFiles splits an artifact's bytes into display files. A `=== FILE
// ===` envelope or a stored JSON manifest becomes a tree; anything else is one
// file whose label falls back to the artifact kind.
func artifactFiles(kind, capability, content string) []artifactFile {
	if files, err := toolenvelope.ParseFileMarkers(content); err == nil && len(files) > 0 {
		return toArtifactFiles(files)
	}
	if toolenvelope.IsTreeManifest([]byte(content)) {
		if files, err := toolenvelope.DecodeFiles([]byte(content)); err == nil {
			return toArtifactFiles(files)
		}
	}
	name := "artifact"
	switch artifactModeFor(kind, capability) {
	case modeCode:
		name = "solution.txt"
	case modeDataset:
		name = "data.json"
	case modeConfig:
		name = "config.yaml"
	}
	return []artifactFile{{Path: name, Content: content}}
}

func toArtifactFiles(files []toolenvelope.File) []artifactFile {
	out := make([]artifactFile, 0, len(files))
	for _, f := range files {
		out = append(out, artifactFile{Path: f.Path, Content: f.Content})
	}
	return out
}

// downloadName is the suggested filename for an artifact. Multi-file code is
// not a single file on disk; the download is the raw stored bytes, so the name
// says so rather than pretending to be one module.
func downloadName(a artifact.Artifact) string {
	ext := ".txt"
	switch a.Kind {
	case artifact.KindDataset:
		ext = ".json"
	case artifact.KindConfiguration:
		ext = ".yaml"
	case artifact.KindMedia:
		ext = ".bin"
	case artifact.KindDocument:
		ext = ".md"
	case artifact.KindProposal:
		ext = ".txt"
	}
	short := a.ID
	if len(short) > 8 {
		short = short[:8]
	}
	return "athanor-" + string(a.Kind) + "-v" + itoa(a.Version) + "-" + short + ext
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// artifactContentType is the download media type. Everything is served as
// inert text or an opaque binary; the browser must never render user/model
// output as HTML.
func artifactContentType(k artifact.Kind) string {
	switch k {
	case artifact.KindDocument:
		return "text/markdown; charset=utf-8"
	case artifact.KindDataset:
		return "application/json; charset=utf-8"
	case artifact.KindConfiguration:
		return "application/yaml; charset=utf-8"
	case artifact.KindMedia:
		return "application/octet-stream"
	default:
		return "text/plain; charset=utf-8"
	}
}

// fileDiff is one file's line diff. Code trees are compared file by file so a
// changed module does not read as one giant unstructured blob.
type fileDiff struct {
	Path  string
	Lines []diffLine
}

// fileDiffs compares two artifact bodies as trees when both parse, and as a
// single stream otherwise. The union of paths is sorted so the output is
// deterministic; a removed or added file appears as a full-file diff.
func fileDiffs(kind, capability, oldText, newText string) []fileDiff {
	oldFiles := artifactFiles(kind, capability, oldText)
	newFiles := artifactFiles(kind, capability, newText)
	if len(oldFiles) == 1 && len(newFiles) == 1 &&
		oldFiles[0].Path == newFiles[0].Path && oldFiles[0].Path == "artifact" {
		return []fileDiff{{Path: "", Lines: lineDiff(oldText, newText)}}
	}
	oldByPath := make(map[string]string, len(oldFiles))
	newByPath := make(map[string]string, len(newFiles))
	paths := make(map[string]bool, len(oldFiles)+len(newFiles))
	for _, f := range oldFiles {
		oldByPath[f.Path] = f.Content
		paths[f.Path] = true
	}
	for _, f := range newFiles {
		newByPath[f.Path] = f.Content
		paths[f.Path] = true
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)
	out := make([]fileDiff, 0, len(ordered))
	for _, p := range ordered {
		out = append(out, fileDiff{Path: p, Lines: lineDiff(oldByPath[p], newByPath[p])})
	}
	return out
}

// artifactBranch is the agent branch a code artifact's commit landed on
// (ADR-0030). Empty when the project has no repository or the artifact was
// never committed.
func artifactBranch(a artifact.Artifact, p project.Project) string {
	if a.GitCommit == "" || strings.TrimSpace(p.RepositoryPath) == "" {
		return ""
	}
	return "athanor/" + p.ID
}
