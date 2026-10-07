package strategy

// Canonical cognitive-operation identifiers (M8-T7; docs/cognitive-operations.md,
// ADR-0064). The vocabulary is closed and shared: the engine records the
// operations a job executed under these names, and the M8 selection layer
// learns per-operation. Adding an operation is a deliberate catalog edit —
// see the table in docs/cognitive-operations.md §2.
const (
	OpPlan       = "plan"
	OpResearch   = "research"
	OpDraft      = "draft"
	OpDiverge    = "diverge"
	OpVerify     = "verify"
	OpReflect    = "reflect"
	OpSynthesize = "synthesize"
	OpCompare    = "compare"
	OpCompress   = "compress"
	OpCommit     = "commit"
)

// Operation is one executed cognitive operation in a job's trajectory: what
// ran, how much it cost, and — when a deterministic verifier judged it —
// whether it passed. Cost is coarse and attributed (model calls and tokens)
// so selection can compare operations without a per-operation profiler;
// Grounded/Passed carry the objective signal (verification, real tests) that
// the trajectory learns from. Grounded false means no deterministic verdict
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

// nonNilOperations normalizes a nil trajectory to an empty slice so the JSON
// column is always a valid array.
func nonNilOperations(ops []Operation) []Operation {
	if ops == nil {
		return []Operation{}
	}
	return ops
}

// ValidOperation reports whether name is in the canonical vocabulary.
func ValidOperation(name string) bool {
	switch name {
	case OpPlan, OpResearch, OpDraft, OpDiverge, OpVerify, OpReflect,
		OpSynthesize, OpCompare, OpCompress, OpCommit:
		return true
	default:
		return false
	}
}
