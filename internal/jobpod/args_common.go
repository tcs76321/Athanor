//go:build linux || darwin

package jobpod

import (
	"strconv"
	"strings"
)

// buildArgs constructs the `podman run` argv for a Spec. The result
// is the full argv, beginning with "run". Per-platform hardening is
// supplied by platformHardening (see args_linux.go and args_darwin.go).
func buildArgs(spec Spec) []string {
	limits := withDefaults(spec.ResourceLimits)
	args := []string{
		"run", "--rm",
		"--name", containerNamePrefix + spec.ID,
		"--detach",
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", itoa(limits.PidsLimit),
		"--memory", itoa(limits.MemoryMB) + "m",
		"--cpus", ftoa(limits.CPUs),
		"--network", "none",
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=" + itoa(limits.TmpfsMB) + "m",
	}
	args = append(args, platformHardening()...)
	if spec.MountToken {
		tokenMount := spec.TokenMountDir
		if tokenMount == "" {
			tokenMount = spec.TokenDir
		}
		args = append(args, mountFlags(tokenMount)...)
	}
	if spec.RepoDir != "" {
		repoMount := spec.RepoMountDir
		if repoMount == "" {
			repoMount = spec.RepoDir
		}
		args = append(args, "--mount", "type=bind,source="+repoMount+",target=/workspace/repo,ro")
	}
	// known-issues L11: `--` ends option parsing so the image is always the
	// first positional argument, even if it begins with '-'.
	args = append(args, "--")
	args = append(args, spec.Image)
	args = append(args, spec.Command...)
	return args
}

// withDefaults fills zero-valued limits with the documented defaults.
func withDefaults(l Limits) Limits {
	if l.PidsLimit < 1 {
		l.PidsLimit = 64
	}
	if l.MemoryMB < 4 {
		l.MemoryMB = 512
	}
	if l.CPUs <= 0 {
		l.CPUs = 1.0
	}
	if l.TmpfsMB <= 0 {
		l.TmpfsMB = 256
	}
	return l
}

// mountFlags builds the per-job token bind mount flags. The token
// dir is bind-mounted read-only at /run/athanor; the pod reads
// /run/athanor/token to authenticate against the internal API.
func mountFlags(tokenDir string) []string {
	if tokenDir == "" {
		return nil
	}
	return []string{
		"--mount", "type=bind,source=" + tokenDir + ",target=/run/athanor,ro",
	}
}

// joinArgs concatenates argv for substring assertions in tests.
func joinArgs(args []string) string { return strings.Join(args, " ") }

// itoa / ftoa format the integer and fractional resource limits for the
// podman argv. ftoa must preserve every digit the operator chose: the UI
// accepts 0.25-step CPU values, and truncating 1.25 to 1.2 silently
// under-allocates the pod.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func ftoa(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
