// Package artifact implements the artifact model and lifecycle
// (ARCHITECTURE §9): draft creation, versioning via supersede chains,
// per-project listing, and the §9.3 status flow.
//
// Artifact content lives as files under a caller-provided directory
// (state/artifacts); every row records a SHA-256 content hash so bitrot
// is detectable on read.
package artifact

import (
	"fmt"
	"time"
)

// Kind is an artifact type (§9.1). The set is closed and matches the
// artifacts.kind CHECK (migration 0003, ADR-0005).
type Kind string

// The §9.1 artifact kinds.
const (
	KindCode          Kind = "code"
	KindDocument      Kind = "document"
	KindDataset       Kind = "dataset"
	KindProposal      Kind = "proposal"
	KindEvaluation    Kind = "evaluation"
	KindMedia         Kind = "media"
	KindConfiguration Kind = "configuration"
)

// Valid reports whether k is one of the §9.1 kinds.
func (k Kind) Valid() bool {
	switch k {
	case KindCode, KindDocument, KindDataset, KindProposal,
		KindEvaluation, KindMedia, KindConfiguration:
		return true
	default:
		return false
	}
}

// Status is an artifact lifecycle status (§9.3).
type Status string

// The §9.3 artifact statuses.
const (
	StatusDraft      Status = "draft"
	StatusCandidate  Status = "candidate"
	StatusAccepted   Status = "accepted"
	StatusRejected   Status = "rejected"
	StatusQuarantine Status = "quarantined"
	StatusSuperseded Status = "superseded"
)

// Terminal reports whether no further §9.3 transitions may leave the
// status. Superseded is terminal in the status flow — replacement happens
// through NewVersion, not SetStatus.
func (s Status) Terminal() bool {
	switch s {
	case StatusAccepted, StatusRejected, StatusQuarantine, StatusSuperseded:
		return true
	default:
		return false
	}
}

// statusFlow is the §9.3 diagram:
//
//	draft → candidate → accepted
//	                   → rejected
//	draft/candidate → quarantined
//	accepted → superseded
//
// M3-T3 commit 3.2 added the accepted → superseded edge so
// the engine's comparison phase can atomically promote a
// candidate to accepted and demote the previous accepted
// to superseded in a single transaction (the §9.3
// "at most one accepted artifact per project" invariant).
// NewVersion is the alternative for cases where a
// non-comparison-phase caller needs the same effect
// without going through SupersedeAndAccept.
var statusFlow = map[Status][]Status{
	StatusDraft:     {StatusCandidate, StatusQuarantine},
	StatusCandidate: {StatusAccepted, StatusRejected, StatusQuarantine},
	StatusAccepted:  {StatusSuperseded},
}

// CanTransition reports whether from → to is a legal §9.3 move.
func CanTransition(from, to Status) bool {
	for _, candidate := range statusFlow[from] {
		if candidate == to {
			return true
		}
	}
	return false
}

// IllegalStatusError names both statuses.
type IllegalStatusError struct {
	From, To Status
}

func (e *IllegalStatusError) Error() string {
	return fmt.Sprintf("illegal artifact status transition %s → %s", e.From, e.To)
}

// ValidateTransition returns a typed error when from → to is illegal.
func ValidateTransition(from, to Status) error {
	if !CanTransition(from, to) {
		return &IllegalStatusError{From: from, To: to}
	}
	return nil
}

// Artifact is one persisted artifact row (§9.2).
type Artifact struct {
	ID           string
	ProjectID    string
	TaskID       string // optional
	JobID        string // optional
	SupersedesID string // optional; set by NewVersion
	Kind         Kind
	Version      int
	Status       Status
	StoragePath  string
	ContentHash  string
	// GitCommit is the SHA of the commit an accepted artifact was recorded
	// to on an agent branch (§9.2, §14; ADR-0030). Empty until acceptance
	// records it, or when the project has no repository.
	GitCommit string
	CreatedAt time.Time
	UpdatedAt time.Time
}
