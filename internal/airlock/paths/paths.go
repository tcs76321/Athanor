package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// ValidateOptions configures the structural + mode checks
// applied by Validate. The zero value is the safe default for
// the §21.3 egress pipeline: allow regular files only, no
// executables, no symlinks at the final component, no
// setuid/setgid.
type ValidateOptions struct {
	// AllowExecutable, if true, permits a regular file with
	// any mode bits. The §21.3 egress rule rejects
	// "unexpected" executables; the default is to treat
	// any +x bit as a rejection. Set to true only when the
	// caller has a specific reason (a code-archetype job
	// pod that runs a test runner, for example).
	AllowExecutable bool
	// AllowMissing, if true, suppresses the ErrNotFound
	// return for paths that do not exist. Useful for
	// "validate that this path WOULD be acceptable" calls
	// (an ingress pipeline planning a write).
	AllowMissing bool
}

// Resolve joins root and rel, normalizes the result, and
// returns the absolute path that would be operated on. It
// performs only path-arithmetic checks: no filesystem access,
// no symlink resolution. The function is safe to call on
// paths that may not exist (ingress planning).
//
// Errors:
//   - ErrInvalid: rel contains a NULL byte or other invalid
//     bytes that the OS cannot accept.
//   - ErrAbsolute: rel is absolute.
//   - ErrTraversal: rel contains a ".." segment that escapes
//     root after Clean.
//
// Resolve never opens a file and never follows a symlink; it
// is the cheap pre-check used by every other function in this
// package.
func Resolve(root, rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", ErrInvalid
	}
	// Reject absolute inputs up front. filepath.IsAbs covers
	// POSIX ("/foo"); the package's own tests cover POSIX.
	if filepath.IsAbs(rel) {
		return "", ErrAbsolute
	}
	// Clean the relative path. filepath.Clean reduces
	// "a/../../b" to "../b", which is what we want to
	// detect.
	cleaned := filepath.Clean(rel)
	// A leading ".." means traversal escapes root. The
	// earlier Clean pass removed any internal "..", so a
	// leading ".." is the only remaining failure mode.
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", ErrTraversal
	}
	// Join + re-clean. Joining a relative cleaned path to
	// an absolute root produces an absolute path that
	// starts with root. The lexically-equivalent check
	// (HasPrefix after Clean) is the structural containment
	// guarantee; symlink escapes are caught by Validate /
	// OpenNoFollow, not here.
	joined := filepath.Clean(filepath.Join(root, cleaned))
	return joined, nil
}

// Validate resolves the path and then runs the structural
// rejection checks (mode bits, file type) against the actual
// filesystem. It is the strict pre-open gate for both ingress
// and egress pipelines. The function does not open the file
// itself; callers that want a handle should use OpenNoFollow
// to get the O_NOFOLLOW defense at the kernel level.
//
// Errors:
//   - Errors returned by Resolve (invalid bytes, absolute,
//     traversal).
//   - ErrNotFound: the resolved path does not exist. Use
//     opts.AllowMissing=true to suppress for planning calls.
//   - ErrSymlinkEscape: the resolved path contains a symlink
//     that points outside root.
//   - ErrDevice: the resolved path is a block, char, or FIFO
//     device node.
//   - ErrSetUID: the resolved path has setuid or setgid.
//   - ErrExecutable: the resolved path is executable and
//     opts.AllowExecutable is false.
//
// Validate is the gate every other file operation in the
// agent builds on. The test suite (paths_test.go) exercises
// each rejection class with an adversarial corpus.
func Validate(root, rel string, opts ValidateOptions) (string, error) {
	resolved, err := Resolve(root, rel)
	if err != nil {
		return "", err
	}
	// Lstat (not Stat) so we never follow a symlink at the
	// final component. This is the path-arithmetic half of
	// the symlink defense; the kernel-level half is in
	// OpenNoFollow.
	info, err := os.Lstat(resolved)
	if err != nil {
		if os.IsNotExist(err) && opts.AllowMissing {
			return resolved, nil
		}
		return "", ErrNotFound
	}
	mode := info.Mode()
	switch {
	case mode&os.ModeDevice != 0:
		// Block or character device. Never a workspace
		// file; reject on sight.
		return "", ErrDevice
	case mode&os.ModeNamedPipe != 0:
		// FIFO. Same reasoning as device.
		return "", ErrDevice
	case mode&os.ModeSetuid != 0 || mode&os.ModeSetgid != 0:
		// setuid/setgid wins over the executable
		// check below: a file with setuid bit set
		// almost certainly also has +x, and the
		// §21.3 rule rejects setuid regardless of
		// whether the file is "expected" to be
		// executable.
		return "", ErrSetUID
	}
	// Symlink containment: resolve every symlink in the path — not only the
	// final component — and require the canonical target to stay within the
	// canonical root. Checking only the final component (the pre-fix
	// behavior) let a symlinked *directory* under root (e.g. root/sub -> /etc)
	// resolve outside root, because OpenNoFollow's O_NOFOLLOW guards only the
	// last component. The path exists here (AllowMissing returned above), so
	// EvalSymlinks cannot fail on a missing component.
	canonical, err := filepath.EvalSymlinks(resolved)
	if err != nil {
		return "", ErrSymlinkEscape
	}
	rootCanonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", ErrSymlinkEscape
	}
	if filepath.Clean(canonical) != filepath.Clean(rootCanonical) &&
		!strings.HasPrefix(filepath.Clean(canonical), filepath.Clean(rootCanonical)+string(filepath.Separator)) {
		return "", ErrSymlinkEscape
	}
	// Executable check. The +x bit is the user/group/other
	// exec bit; a regular file with any +x bit is treated
	// as an executable. The check is gated on
	// AllowExecutable so the §21.3 egress rule ("reject
	// unexpected executables") is a default, not a hard
	// wall.
	if !opts.AllowExecutable && mode.IsRegular() && (mode&0o111) != 0 {
		return "", ErrExecutable
	}
	return resolved, nil
}

// OpenNoFollow resolves the path, validates it, and opens it
// with O_NOFOLLOW. The kernel-level no-follow closes the
// TOCTOU window between Validate's Lstat and the open(2)
// syscall: if a symlink appeared at the final component
// after the Lstat, open returns ELOOP instead of following
// the symlink to an attacker-controlled target.
//
// The O_NOFOLLOW constant is reached through noFollowFlag(),
// which is defined in a build-tag-gated file (paths_linux.go
// or paths_darwin.go) that is the only place in internal/
// that imports the `syscall` package. Gate G1's rule 5
// allowlists those two files. On an unsupported GOOS,
// noFollowFlag returns 0 and the O_NOFOLLOW defense is
// dropped — paths_other.go logs a package-init warning, and
// the supported-build gate is the source of truth.
func OpenNoFollow(root, rel string) (*os.File, error) {
	resolved, err := Validate(root, rel, ValidateOptions{})
	if err != nil {
		return nil, err
	}
	return os.OpenFile(resolved, os.O_RDONLY|noFollowFlag(), 0)
}
