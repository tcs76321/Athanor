package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// execClient is the production jobpod.Client. It shells out to the
// `podman` binary on the host's PATH. Lives in cmd/ so that
// internal/jobpod itself has no os/exec import (and so
// internal/gate/gate_test.go stays clean).
//
// Usage:
//
//	mgr := jobpod.New(jobpodClient(), killSwitch)
type execClient struct{}

// NewExecClient returns a jobpod.Client that runs `podman` on PATH.
// The client honors ctx cancellation: a canceled call returns
// ctx.Err() wrapped in the exit error.
func NewExecClient() *execClient { return &execClient{} }

// maxPodStreamBytes bounds one stdout or stderr stream buffered from a
// `podman exec`. Without a cap a chatty test command (or a runaway model
// candidate) can make the Core buffer gigabytes and OOM. The command still
// runs to completion; the stream keeps its head and records that it was
// truncated.
const maxPodStreamBytes = 256 << 10

// cappedStream is an io.Writer that keeps at most limit bytes and records
// whether more was written. It never reports a short write to the child, so
// the command does not receive EPIPE or fail because the Core stopped
// buffering.
type cappedStream struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (c *cappedStream) Write(p []byte) (int, error) {
	if c.limit <= 0 {
		c.limit = maxPodStreamBytes
	}
	remaining := c.limit - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	_, _ = c.buf.Write(p)
	return len(p), nil
}

// Bytes returns the retained prefix plus a marker when the stream was
// truncated. It returns a copy so callers cannot mutate the buffer.
func (c *cappedStream) Bytes() []byte {
	out := append([]byte(nil), c.buf.Bytes()...)
	if c.truncated {
		out = append(out, []byte("\n[athanor: output truncated]\n")...)
	}
	return out
}

// Run implements jobpod.Client.
func (c *execClient) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	// Prepend "podman" to the argv. The jobpod package treats args
	// as the argv after `podman` (e.g. ["run", "--rm", ...]).
	full := append([]string{"podman"}, args...)
	cmd := exec.CommandContext(ctx, "podman", args...)

	stdout := &cappedStream{limit: maxPodStreamBytes}
	stderr := &cappedStream{limit: maxPodStreamBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err != nil {
		// Distinguish cancellation (deliberate stop) from a real
		// podman failure; the jobpod manager handles them the same
		// way (retry-or-give-up), but the message is clearer here.
		if ctx.Err() != nil {
			return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("podman canceled: %w", ctx.Err())
		}
		// exec.ExitError wraps the exit code; surface it as part
		// of the error so callers can branch on it.
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("podman %s: %w", full[1], err)
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

// RunStdin implements jobpod.Client. It is Run with the command's
// standard input wired to stdin, used by `podman exec -i` so
// execute_code can pipe source without exposing it in argv (ADR-0024
// §3). A nil/empty stdin behaves exactly like Run.
func (c *execClient) RunStdin(ctx context.Context, stdin []byte, args ...string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, "podman", args...)
	if len(stdin) > 0 {
		cmd.Stdin = bytes.NewReader(stdin)
	}

	stdout := &cappedStream{limit: maxPodStreamBytes}
	stderr := &cappedStream{limit: maxPodStreamBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("podman canceled: %w", ctx.Err())
		}
		verb := ""
		if len(args) > 0 {
			verb = args[0]
		}
		return stdout.Bytes(), stderr.Bytes(), fmt.Errorf("podman %s: %w", verb, err)
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}
