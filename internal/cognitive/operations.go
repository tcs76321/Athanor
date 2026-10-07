// Package cognitive is the shared cognitive-operation vocabulary (M8-T6/T7,
// ADR-0064; docs/cognitive-operations.md). It is a deliberate leaf: both
// internal/policy (selection) and internal/strategy (trajectory capture)
// import it, and it imports only the standard library — so the vocabulary has
// one source of truth while the policy seam stays free of the store/DB
// dependency that the strategy package carries.
package cognitive

// Canonical operation identifiers (ADR-0064; docs/cognitive-operations.md §2).
// The vocabulary is closed and shared: the engine records the operations a job
// executed under these names, and the selection layer (M8-T8) chooses from
// them. Adding an operation is a deliberate catalog edit.
const (
	OpPlan       = "plan"
	OpResearch   = "research"
	OpDraft      = "draft"
	OpDiverge    = "diverge"
	OpVerify     = "verify"
	OpRefine     = "refine"
	OpReflect    = "reflect"
	OpSynthesize = "synthesize"
	OpCompare    = "compare"
	OpCompress   = "compress"
	OpCommit     = "commit"
)

// BaselineOperations is the phase-derived set a default job is eligible to run,
// in canonical order. It is the starting point the selection seam (M8-T8)
// learns to narrow or extend; it does not include the conditional operations
// (research, commit) or the off-job operations (compress, daydream).
var BaselineOperations = []string{
	OpPlan, OpDiverge, OpVerify, OpReflect, OpSynthesize, OpCompare,
}

// ExecutionOrder is the canonical emission order for a job's trajectory
// (M8-T7). Capture emits operations in this order so trajectories are
// comparable across jobs; an operation absent from the run is simply omitted.
var ExecutionOrder = []string{
	OpResearch, OpPlan, OpDraft, OpDiverge, OpVerify, OpRefine, OpReflect,
	OpSynthesize, OpCompare, OpCompress, OpCommit,
}

// Operation is one executed cognitive operation in a job's trajectory: what
// ran, how much it cost, and — when a deterministic verifier judged it —
// whether it passed. Cost is coarse and attributed (model calls and tokens) so
// selection can compare operations without a per-operation profiler;
// Grounded/Passed carry the objective signal (verification, real tests) the
// trajectory learns from. Grounded false means no deterministic verdict
// applied (the operation is advisory only).
type Operation struct {
	Name     string `json:"name"`
	Phase    string `json:"phase,omitempty"`
	Persona  string `json:"persona,omitempty"`
	Calls    int    `json:"calls,omitempty"`
	Tokens   int    `json:"tokens,omitempty"`
	Grounded bool   `json:"grounded,omitempty"`
	Passed   bool   `json:"passed,omitempty"`
}

// ValidOperation reports whether name is in the canonical vocabulary.
func ValidOperation(name string) bool {
	switch name {
	case OpPlan, OpResearch, OpDraft, OpDiverge, OpVerify, OpRefine, OpReflect,
		OpSynthesize, OpCompare, OpCompress, OpCommit:
		return true
	default:
		return false
	}
}

// NonNil normalizes a nil operation slice to an empty slice so a JSON column is
// always a valid array.
func NonNil(ops []Operation) []Operation {
	if ops == nil {
		return []Operation{}
	}
	return ops
}
