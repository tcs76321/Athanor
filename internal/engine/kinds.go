package engine

import (
	"github.com/tcs76321/athanor/internal/artifact"
	"github.com/tcs76321/athanor/internal/project"
)

// finalKindFor maps a task capability to the artifact kind its final output
// takes (§9.1). The phase package has its own copy; the root keeps one for the
// terminal process scorecard's final-artifact lookup.
func finalKindFor(capability string) artifact.Kind {
	switch capability {
	case project.CapabilityCode:
		return artifact.KindCode
	case project.CapabilityData:
		return artifact.KindDataset
	case project.CapabilityMedia:
		return artifact.KindMedia
	default: // text, document
		return artifact.KindDocument
	}
}
