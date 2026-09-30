package jobpod

import "errors"

// ErrInvalidSpec reports a Spec that failed validation. Callers
// should not retry without fixing the spec.
var ErrInvalidSpec = errors.New("jobpod: invalid spec")

// ErrFrozen reports the daemon is frozen (§22.1: no new work).
// Callers should not retry until the daemon is unfrozen.
var ErrFrozen = errors.New("jobpod: daemon is frozen")

// ErrNotFound reports an unknown pod ID. Distinct from a not-yet-
// started ID, which is ErrInvalidSpec.
var ErrNotFound = errors.New("jobpod: pod not found")

// ErrAlreadyExists reports a Start call with an ID that is already
// running or pending.
var ErrAlreadyExists = errors.New("jobpod: pod with that ID already exists")

// ErrNotRunning reports an Exec against a pod that cannot accept
// commands (stopped or failed).
var ErrNotRunning = errors.New("jobpod: pod is not running")
