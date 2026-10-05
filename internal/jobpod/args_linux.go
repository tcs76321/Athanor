//go:build linux

package jobpod

// platformHardening returns the §21.2 hardening flag set for Linux.
// seccomp is meaningful here (the kernel enforces it). Podman does
// not accept Docker/containerd's "runtime/default" keyword: any value
// other than "unconfined" is treated as a file path, so
// "runtime/default" resolves to a nonexistent file and the pod fails
// to start. Point at the containers-common default profile
// explicitly; if it is ever absent, podman fails closed rather than
// silently running unconfined.
func platformHardening() []string {
	return []string{
		"--security-opt", "seccomp=/usr/share/containers/seccomp.json",
	}
}
