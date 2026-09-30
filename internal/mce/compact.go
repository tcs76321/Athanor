package mce

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/tcs76321/athanor/internal/store"
)

// Temperature-0.0 compaction (ARCHITECTURE §10.1–§10.3; ROADMAP M5-T6;
// ADR-0025).
//
// §10.3 is the only place the MCE may lose information, so this file is
// deliberately narrow: it classifies a memory item's §10.2 profile, decides
// whether the item may be compacted at all, and persists the result under a
// content-address key so the same input always yields the same output without a
// model call. The model call itself is a seam (Compactor), whose LLM-backed
// adapter lives in cmd/ — internal/mce never imports internal/llm (ADR-0021 §2).

// EpistemicType is §10.2's first axis. The set is closed; §10.3's
// "brainstorming" row is the conversation type.
type EpistemicType string

const (
	EpistemicCode          EpistemicType = "code"
	EpistemicTestOutput    EpistemicType = "test_output"
	EpistemicConversation  EpistemicType = "conversation"
	EpistemicLog           EpistemicType = "log"
	EpistemicDocumentation EpistemicType = "documentation"
)

// TemporalState is §10.2's second axis.
type TemporalState string

const (
	TemporalActive   TemporalState = "active"
	TemporalRecent   TemporalState = "recent"
	TemporalEpisodic TemporalState = "episodic"
	TemporalArchival TemporalState = "archival"
)

// Profile is one memory item's §10.2 coordinates.
type Profile struct {
	Type  EpistemicType
	State TemporalState
}

// String renders the profile as "type/state" for audit rows and the prompt
// header.
func (p Profile) String() string { return string(p.Type) + "/" + string(p.State) }

// CompactionKind selects the §10.3 mechanism.
type CompactionKind string

const (
	// CompactionDeterministic extracts exact error codes, stack traces, and
	// return values from logs and test output (§10.3).
	CompactionDeterministic CompactionKind = "deterministic"
	// CompactionSemantic extracts decisions, constraints, derived rules, key
	// facts, and API signatures from conversations and documentation (§10.3).
	CompactionSemantic CompactionKind = "semantic"
)

// TreatmentMode is §10.3's treatment for a profile.
type TreatmentMode string

const (
	// ModeDivision is full-fidelity structural division: lossless, no LLM.
	ModeDivision TreatmentMode = "division"
	// ModeCompaction is a Temp 0.0 lossy pass.
	ModeCompaction TreatmentMode = "compaction"
)

// Treatment is the §10.3 treatment matrix's decision for one profile.
type Treatment struct {
	Mode TreatmentMode
	Kind CompactionKind // set only when Mode == ModeCompaction
}

// Compactable reports whether the treatment is a compaction.
func (t Treatment) Compactable() bool { return t.Mode == ModeCompaction }

// TreatmentFor applies §10.3's treatment matrix.
//
// The default is Division: only the rows §10.3 explicitly assigns to compaction
// are compacted; everything else keeps full fidelity. "When unsure, divide" is
// the only direction that cannot lose bytes. Source code is never compacted
// (§10.1: code is divided, never summarized).
func TreatmentFor(p Profile) Treatment {
	if p.Type == EpistemicCode {
		return Treatment{Mode: ModeDivision}
	}
	switch p.State {
	case TemporalActive, TemporalRecent:
		return Treatment{Mode: ModeDivision}
	case TemporalEpisodic:
		switch p.Type {
		case EpistemicLog, EpistemicTestOutput:
			return Treatment{Mode: ModeCompaction, Kind: CompactionDeterministic}
		case EpistemicDocumentation:
			return Treatment{Mode: ModeCompaction, Kind: CompactionSemantic}
		}
	case TemporalArchival:
		switch p.Type {
		case EpistemicConversation, EpistemicDocumentation:
			return Treatment{Mode: ModeCompaction, Kind: CompactionSemantic}
		}
	}
	return Treatment{Mode: ModeDivision}
}

// The §10.3 invariant constants.
const (
	// CompactionPersona is the §10.3 persona for every compaction.
	CompactionPersona = "security"
	// CompactionTemperature is the §10.3 invariant temperature.
	CompactionTemperature = 0.0
)

// ErrNotCompactable reports that a profile is full-fidelity: the caller must
// divide it, not compact it (§10.3, ADR-0025 §2).
var ErrNotCompactable = errors.New("mce: profile is full-fidelity; divide, do not compact")

// ErrCompactorNeeded reports a nil Compactor seam.
var ErrCompactorNeeded = errors.New("mce: compaction requires a Compactor")

// MemoryItem is one unit of compactable memory.
type MemoryItem struct {
	// Content is the raw bytes to compact.
	Content []byte
	// Profile is the §10.2 classification that selects the treatment.
	Profile Profile
	// Ref attributes the item to a project/job/path (optional).
	Ref SourceRef
	// SourceHash is the origin's content hash (optional); it lets a source skip
	// an item already consolidated.
	SourceHash string
}

// Compactor produces a compaction at Temp 0.0 on the §10.3 security persona.
// The interface lives in internal/mce; its LLM-backed adapter is in cmd/.
type Compactor interface {
	// Compact returns the compacted text for one item. Implementations must use
	// CompactionPersona and CompactionTemperature.
	Compact(ctx context.Context, item MemoryItem, kind CompactionKind) (string, error)
	// TemplateVersion names the prompt template for a kind. It is part of the
	// content-address key (ADR-0025 §1), so a template change yields a new row
	// rather than reusing stale output.
	TemplateVersion(kind CompactionKind) string
}

// CompactionResult reports one CompactMemory outcome.
type CompactionResult struct {
	Kind           CompactionKind
	InputHash      string
	Content        string
	Cached         bool // true when the content-address hit and no model ran
	SourceBytes    int
	CompactedBytes int
}

// CompactStore persists compacted memory (migration 0012). It is a distinct
// type from ChunkStore so the division/swap store stays focused (ADR-0025 §4).
type CompactStore struct{ db *store.Store }

// NewCompactStore returns a CompactStore over the daemon's single database.
func NewCompactStore(s *store.Store) *CompactStore { return &CompactStore{db: s} }

// CompactMemory compacts one item, or returns the previously stored output for
// an identical input. Determinism is content-addressed (ADR-0025 §1): the input
// hash is part of the key, so a second call for the same input never reaches
// the model and returns byte-identical content.
//
// A non-compactable profile is ErrNotCompactable; a nil seam is
// ErrCompactorNeeded; a compactor error persists nothing (retryable).
func (c *CompactStore) CompactMemory(ctx context.Context, item MemoryItem, comp Compactor) (CompactionResult, error) {
	if comp == nil {
		return CompactionResult{}, ErrCompactorNeeded
	}
	tr := TreatmentFor(item.Profile)
	if !tr.Compactable() {
		return CompactionResult{}, fmt.Errorf("%w: profile %s", ErrNotCompactable, item.Profile)
	}
	kind := tr.Kind
	version := comp.TemplateVersion(kind)
	inputHash := compactionInputHash(kind, version, item)

	if res, found, err := c.lookupCompaction(ctx, kind, inputHash); err != nil {
		return CompactionResult{}, err
	} else if found {
		res.Cached = true
		return res, nil
	}

	content, err := comp.Compact(ctx, item, kind)
	if err != nil {
		return CompactionResult{}, fmt.Errorf("mce: compact profile %s: %w", item.Profile, err)
	}

	sourceBytes := len(item.Content)
	inserted, err := c.insertCompaction(ctx, kind, version, item, inputHash, content, sourceBytes)
	if err != nil {
		return CompactionResult{}, err
	}
	if !inserted {
		// Lost a race: another writer stored this input first. Return the
		// winner's bytes so the result is stable and no second audit row is
		// written for one input.
		if winner, found, err := c.lookupCompaction(ctx, kind, inputHash); err != nil {
			return CompactionResult{}, err
		} else if found {
			winner.Cached = true
			return winner, nil
		}
	}
	if err := c.auditCompaction(ctx, kind, version, item, inputHash, len(content)); err != nil {
		return CompactionResult{}, err
	}
	return CompactionResult{
		Kind: kind, InputHash: inputHash, Content: content,
		SourceBytes: sourceBytes, CompactedBytes: len(content),
	}, nil
}

// HasSource reports whether an origin content hash already has a compaction of
// the given kind. Used by the daydream sources to skip caught-up work.
func (c *CompactStore) HasSource(ctx context.Context, sourceHash string, kind CompactionKind) (bool, error) {
	if sourceHash == "" {
		return false, nil
	}
	return c.compactionExists(ctx,
		`SELECT 1 FROM compacted_memory WHERE source_hash = ? AND kind = ? LIMIT 1`, sourceHash, string(kind))
}

// HasJob reports whether a job already has a compaction of the given kind.
func (c *CompactStore) HasJob(ctx context.Context, jobID string, kind CompactionKind) (bool, error) {
	if jobID == "" {
		return false, nil
	}
	return c.compactionExists(ctx,
		`SELECT 1 FROM compacted_memory WHERE job_id = ? AND kind = ? LIMIT 1`, jobID, string(kind))
}

// compactionInputHash is the content-address key: every input that could change
// the output is included (kind, template version, persona, temperature, profile,
// and the content hash), so a change to any of them yields a new row.
func compactionInputHash(kind CompactionKind, version string, item MemoryItem) string {
	key := strings.Join([]string{
		string(kind),
		version,
		CompactionPersona,
		strconv.FormatFloat(CompactionTemperature, 'f', -1, 64),
		item.Profile.String(),
		hashBytes(item.Content),
	}, "\x00")
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// compactionID derives the row id from kind + input hash (ADR-0021 §5 pattern).
func compactionID(kind CompactionKind, inputHash string) string {
	sum := sha256.Sum256([]byte(string(kind) + "\x00" + inputHash))
	return "cm-" + hex.EncodeToString(sum[:])[:32]
}

// lookupCompaction reads the stored output for a content-address key.
func (c *CompactStore) lookupCompaction(ctx context.Context, kind CompactionKind, inputHash string) (CompactionResult, bool, error) {
	var content string
	var sourceBytes, compactedBytes int
	err := c.db.DB().QueryRowContext(ctx,
		`SELECT content, source_bytes, compacted_bytes FROM compacted_memory WHERE kind = ? AND input_hash = ?`,
		string(kind), inputHash,
	).Scan(&content, &sourceBytes, &compactedBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return CompactionResult{}, false, nil
	}
	if err != nil {
		return CompactionResult{}, false, fmt.Errorf("mce: read compaction %s: %w", inputHash, err)
	}
	return CompactionResult{
		Kind: kind, InputHash: inputHash, Content: content,
		SourceBytes: sourceBytes, CompactedBytes: compactedBytes,
	}, true, nil
}

// insertCompaction persists a new compaction. The second return is false when
// the row already existed (a racing writer won).
func (c *CompactStore) insertCompaction(ctx context.Context, kind CompactionKind, version string,
	item MemoryItem, inputHash, content string, sourceBytes int) (bool, error) {

	res, err := c.db.DB().ExecContext(ctx, `
		INSERT INTO compacted_memory
		    (id, kind, profile_type, profile_state, input_hash, template_version,
		     persona, temperature, source_hash, source_relpath, project_id, job_id,
		     source_bytes, compacted_bytes, content)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(kind, input_hash) DO NOTHING`,
		compactionID(kind, inputHash), string(kind), string(item.Profile.Type), string(item.Profile.State),
		inputHash, version, CompactionPersona, CompactionTemperature,
		nullIfEmpty(item.SourceHash), nullIfEmpty(item.Ref.RelPath),
		nullIfEmpty(item.Ref.ProjectID), nullIfEmpty(item.Ref.JobID),
		sourceBytes, len(content), content,
	)
	if err != nil {
		return false, fmt.Errorf("mce: insert compaction %s: %w", inputHash, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mce: compaction rows affected: %w", err)
	}
	return n > 0, nil
}

// compactionExists reports whether a one-column existence query returns a row.
func (c *CompactStore) compactionExists(ctx context.Context, query string, args ...any) (bool, error) {
	var one int
	err := c.db.DB().QueryRowContext(ctx, query, args...).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mce: compaction exists: %w", err)
	}
	return true, nil
}

// auditCompaction appends the `context` event for one stored compaction.
func (c *CompactStore) auditCompaction(ctx context.Context, kind CompactionKind, version string,
	item MemoryItem, inputHash string, compactedBytes int) error {

	if _, err := c.db.AppendEvent(ctx, store.Event{
		Category:  "context",
		ProjectID: item.Ref.ProjectID,
		JobID:     item.Ref.JobID,
		Data: map[string]any{
			"event":            "memory_compacted",
			"kind":             string(kind),
			"profile":          item.Profile.String(),
			"input_hash":       inputHash,
			"template_version": version,
			"source_bytes":     len(item.Content),
			"compacted_bytes":  compactedBytes,
		},
	}); err != nil {
		return fmt.Errorf("mce: audit compaction: %w", err)
	}
	return nil
}
