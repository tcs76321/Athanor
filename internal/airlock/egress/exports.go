package egress

import (
	"fmt"
	"path/filepath"

	"github.com/tcs76321/athanor/internal/airlock/paths"
)

// ExportPath returns the on-disk path the exporter
// writes to for a given project identifier, artifact ID,
// and content hash. The hash is the artifact's SHA-256;
// the exporter truncates it to 12 hex chars (48 bits)
// for the directory name, which is enough to disambiguate
// re-exports of the same artifact content (idempotency
// contract) and small enough to keep the directory
// listing human-readable.
//
// Layout: `<workspaceRoot>/exports/<projectID>/<artifactID>-<sha12>`
//
// The project and artifact identifiers are run through the §21.3
// path-containment layer (known-issues L10): today they are store-resolved
// UUIDs, but a future caller passing a slug must not be able to escape the
// exports tree with a traversal or an absolute component. An unsafe component
// is an error, never a silently rewritten path.
//
// The daemon wires `workspaceRoot` from the airlock config
// (`<state-dir>/workspace/exports`).
//
// The caller passes the project's ID (UUID). ARCHITECTURE
// §6.1 describes a project `slug` field; the schema
// (migration 0001) does not yet carry one. Using the
// project ID is a stable, unique, URL-safe fallback;
// the slug field is a follow-up migration.
func ExportPath(workspaceRoot, projectID, artifactID, sha256hex string) (string, error) {
	sha12 := sha256hex
	if len(sha12) > 12 {
		sha12 = sha12[:12]
	}
	exportsRoot := filepath.Join(workspaceRoot, "exports")
	rel := filepath.Join(projectID, artifactID+"-"+sha12)
	resolved, err := paths.Resolve(exportsRoot, rel)
	if err != nil {
		return "", fmt.Errorf("egress: unsafe export path %q/%q: %w", projectID, artifactID, err)
	}
	return resolved, nil
}
