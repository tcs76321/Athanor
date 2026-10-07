//go:build linux || darwin

package jobpod

import "strings"

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
		"--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=8m",
	}
	args = append(args, platformHardening()...)
	args = append(args, mountFlags(spec.TokenDir)...)
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

// itoa / ftoa are tiny stdlib-free integer and float formatters. We
// avoid strconv in this file so the build-tag split is symmetric
// across platforms.
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
	// One decimal place is enough for the values we use (1.0, 0.5).
	whole := int(f)
	frac := int((f - float64(whole)) * 10)
	if frac < 0 {
		frac = -frac
	}
	return itoa(whole) + "." + itoa(frac)
}
