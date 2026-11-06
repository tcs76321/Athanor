// Token directory lifecycle for M2-T3 (ADR-0007, ADR-0008).
//
// Each job gets a 16-byte random hex token. On the live Core→pod exec path the
// Core holds it in memory and presents it to its own internal API; the pod does
// not need it. When a future pod→Core direction requires the token inside the
// pod, Spec.MountToken bind-mounts TokenDir read-only at /run/athanor. The
// daemon never persists the token to SQLite, the EventLog, or an artifact.
//
// Layout:
//
//	<state-dir>/tokens/<job-id>/token   (mode 0600, 32 hex chars)
//
// The jobpod package is the only code that knows this layout. Other
// packages see the token as a string passed in the Spec and never
// touch the filesystem directly.
package jobpod

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// tokenBytes is the source entropy length (16 bytes = 128 bits, hex-
// encoded to 32 chars). Matches ADR-0007's documented format.
const tokenBytes = 16

// tokenFileName is the name of the file inside the token dir that the
// pod reads. The bind mount points the entire dir at /run/athanor; the
// pod then reads /run/athanor/token.
const tokenFileName = "token"

// NewTokenDir creates a fresh host directory under base, named after
// jobID, containing a single file "token" with 0600 perms and 32
// random hex chars. The returned dir is the bind-mount source; the
// returned token is the value the daemon hands to the engine in
// memory (never to disk, never to logs).
//
// base is typically <state-dir>/tokens. The parent directory is
// created with 0700 perms; the per-job dir with 0700. Cleanup is the
// caller's responsibility: call RemoveTokenDir when the pod
// terminates.
func NewTokenDir(base, jobID string) (dir, token string, err error) {
	dir = filepath.Join(base, jobID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", fmt.Errorf("creating token dir: %w", err)
	}
	tok, err := newRandomToken()
	if err != nil {
		// Best-effort cleanup: don't leak the empty dir.
		_ = os.RemoveAll(dir)
		return "", "", err
	}
	path := filepath.Join(dir, tokenFileName)
	if err := os.WriteFile(path, []byte(tok), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", "", fmt.Errorf("writing token file: %w", err)
	}
	return dir, tok, nil
}

// NewToken returns a fresh 32-hex-character token without creating a file.
// The current Core→pod execution path authenticates the Core to its own
// internal API, not the pod, so the secret does not need to be exposed to
// candidate code. Use NewTokenDir only when MountToken is requested.
func NewToken() (string, error) {
	return newRandomToken()
}

// RemoveTokenDir deletes dir. Idempotent: a missing dir is not an
// error. Called from manager.Stop and the supervisor's terminal-state
// branch.
func RemoveTokenDir(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("removing token dir: %w", err)
	}
	return nil
}

// newRandomToken returns 32 hex chars from crypto/rand. crypto/rand
// failing is not a runtime condition on supported platforms; the
// panic is a defensive loud-fail rather than handing out a known-
// weak token.
func newRandomToken() (string, error) {
	var b [tokenBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
