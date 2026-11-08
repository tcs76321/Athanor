package onboard

// Qualification planning is deliberately a subset of the benchmark's job:
// every model must see the same cases, and the run should take as much of the
// remaining budget as it can without letting a slow model starve the others.
//
// The pool screen has three nested depths: Minimal (two cases per capability,
// QualificationMinimal), Balanced (the whole standard ladder), and Complete
// (the whole deep ladder). The legacy one-per-capability core and extra blocks
// remain for the two-role setup page.
type CaseRef struct {
	Kind       string // generator | judge
	ID         string
	Capability string
}

var qualificationCapabilityOrder = []string{
	"general", "code", "data", "document", "text", "media",
	"summarize", "security", "agentry", "instruction", "grounding",
	"repair", "longctx", "operation", "planning", "calibration", "judgment",
}

// AllCaseRefs returns every ladder case in a stable order.
func AllCaseRefs(l Ladder) []CaseRef {
	out := make([]CaseRef, 0, len(l.Generators)+len(l.Judges))
	for _, c := range l.Generators {
		out = append(out, CaseRef{Kind: "generator", ID: c.ID, Capability: c.Capability})
	}
	for _, c := range l.Judges {
		out = append(out, CaseRef{Kind: "judge", ID: c.ID, Capability: c.Capability})
	}
	return out
}

// QualificationCore picks one case per capability, preferring a generator.
// It is the bare minimum every model must complete before scores are compared.
func QualificationCore(l Ladder) []CaseRef {
	seen := map[string]bool{}
	var out []CaseRef
	pick := func(want string) {
		for _, c := range l.Generators {
			if c.Capability == want && !seen["generator|"+c.ID] {
				seen["generator|"+c.ID] = true
				out = append(out, CaseRef{Kind: "generator", ID: c.ID, Capability: c.Capability})
				return
			}
		}
		for _, c := range l.Judges {
			if c.Capability == want && !seen["judge|"+c.ID] {
				seen["judge|"+c.ID] = true
				out = append(out, CaseRef{Kind: "judge", ID: c.ID, Capability: c.Capability})
				return
			}
		}
	}
	for _, capability := range qualificationCapabilityOrder {
		pick(capability)
	}
	// Judgment has no generator capability of its own; a judge pair is the
	// only way to measure it. If no case named "judgment", use the first
	// unclaimed judge so the core still covers the category.
	if len(out) == 0 || !hasCapability(out, "judgment") {
		for _, c := range l.Judges {
			if seen["judge|"+c.ID] {
				continue
			}
			seen["judge|"+c.ID] = true
			out = append(out, CaseRef{Kind: "judge", ID: c.ID, Capability: c.Capability})
			break
		}
	}
	return out
}

func hasCapability(refs []CaseRef, want string) bool {
	for _, ref := range refs {
		if ref.Capability == want {
			return true
		}
	}
	return false
}

// QualificationMinimal picks two cases per capability, preferring generators,
// plus one judge pair so judgment is measured too. It is the smallest screen
// that covers every capability with more than a single example.
func QualificationMinimal(l Ladder) []CaseRef {
	return qualificationDepth(l, 2)
}

// qualificationDepth selects up to perCapability cases per capability in the
// stable capability order, keeping the ladder's case order within a capability
// (generators before judges). It always includes at least one judge pair.
func qualificationDepth(l Ladder, perCapability int) []CaseRef {
	if perCapability < 1 {
		perCapability = 1
	}
	refs := AllCaseRefs(l)
	byCap := map[string][]CaseRef{}
	var order []string
	for _, ref := range refs {
		if _, ok := byCap[ref.Capability]; !ok {
			order = append(order, ref.Capability)
		}
		byCap[ref.Capability] = append(byCap[ref.Capability], ref)
	}
	ordered := make([]string, 0, len(order))
	seen := map[string]bool{}
	for _, capability := range qualificationCapabilityOrder {
		if _, ok := byCap[capability]; ok {
			ordered = append(ordered, capability)
			seen[capability] = true
		}
	}
	for _, capability := range order {
		if !seen[capability] {
			ordered = append(ordered, capability)
		}
	}
	out := make([]CaseRef, 0, perCapability*len(ordered))
	in := map[string]bool{}
	for _, capability := range ordered {
		list := byCap[capability]
		n := perCapability
		if n > len(list) {
			n = len(list)
		}
		for _, ref := range list[:n] {
			out = append(out, ref)
			in[ref.Kind+"|"+ref.ID] = true
		}
	}
	hasJudge := false
	for _, ref := range out {
		if ref.Kind == "judge" {
			hasJudge = true
			break
		}
	}
	if !hasJudge {
		for _, ref := range refs {
			if ref.Kind == "judge" && !in[ref.Kind+"|"+ref.ID] {
				out = append(out, ref)
				break
			}
		}
	}
	return out
}

// QualificationExtra returns the cases not in the core, in stable order.
func QualificationExtra(l Ladder) []CaseRef {
	core := map[string]bool{}
	for _, c := range QualificationCore(l) {
		core[c.Kind+"|"+c.ID] = true
	}
	var out []CaseRef
	for _, c := range AllCaseRefs(l) {
		if !core[c.Kind+"|"+c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// QualificationBlocks splits the extra cases into fixed-size blocks. A block
// is only started for every model or not at all, which keeps the sample equal
// across models while still filling the time budget.
func QualificationBlocks(l Ladder, size int) [][]CaseRef {
	if size < 1 {
		size = 20
	}
	extra := QualificationExtra(l)
	var out [][]CaseRef
	for len(extra) > 0 {
		n := size
		if n > len(extra) {
			n = len(extra)
		}
		out = append(out, append([]CaseRef(nil), extra[:n]...))
		extra = extra[n:]
	}
	return out
}

// BalancedCaseKeys returns the case keys (kind|id) completed by every model.
// Scores are computed only on this intersection, so an interrupted extra block
// cannot advantage the models that happened to run first.
func BalancedCaseKeys(results map[string]bool, models []string) map[string]bool {
	if len(models) == 0 {
		return nil
	}
	// Build case key -> model -> done from the state keys kind|model|id.
	cases := map[string]map[string]bool{}
	for key := range results {
		kind, model, id, found := splitStateKey(key)
		if !found {
			continue
		}
		ck := kind + "|" + id
		if cases[ck] == nil {
			cases[ck] = map[string]bool{}
		}
		cases[ck][model] = true
	}
	out := map[string]bool{}
	for ck, byModel := range cases {
		all := true
		for _, m := range models {
			if !byModel[m] {
				all = false
				break
			}
		}
		if all {
			out[ck] = true
		}
	}
	return out
}

func splitStateKey(key string) (kind, model, id string, ok bool) {
	// Keys are kind|model|id; model ids may contain '|'? They do not in
	// practice (Ollama tags use ':' and '-'), so split at the first and last
	// separators to stay safe.
	first := -1
	last := -1
	for i := 0; i < len(key); i++ {
		if key[i] == '|' {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first <= 0 || last <= first {
		return "", "", "", false
	}
	return key[:first], key[first+1 : last], key[last+1:], true
}
