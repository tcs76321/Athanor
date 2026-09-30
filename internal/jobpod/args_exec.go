//go:build linux || darwin

package jobpod

// buildExecArgs constructs the `podman exec` argv for one tool call
// against a running Job Pod (ADR-0024 §2/§3).
//
// Containment is inherited, not re-declared: the container was created
// by buildArgs with the §21.2 hardening set, and exec adds nothing — no
// filesystem bind, no added capability, no namespace change, no altered
// security profile. The only option ever emitted is `-i`, which wires
// the command's stdin so execute_code can pipe source without putting it
// in argv (ADR-0024 §3). A future change that wants an exec-time option
// must add it here and face Gate G2's exec-argv arm.
func buildExecArgs(podID string, stdin bool, command []string) []string {
	args := []string{"exec"}
	if stdin {
		args = append(args, "-i")
	}
	args = append(args, podID)
	args = append(args, command...)
	return args
}
