package dag

import (
	"fmt"
	"sort"
)

// ErrorKind classifies a graph rejection.
type ErrorKind string

// The closed set of rejection kinds.
const (
	KindEmpty        ErrorKind = "empty"
	KindDuplicateKey ErrorKind = "duplicate_key"
	KindUnknownRef   ErrorKind = "unknown_reference"
	KindSelfRef      ErrorKind = "self_reference"
	KindCycle        ErrorKind = "cycle"
	KindCoverage     ErrorKind = "coverage"
	KindBudget       ErrorKind = "budget"
	KindDepth        ErrorKind = "depth"
	KindTooMany      ErrorKind = "too_many_tasks"
)

// ValidationError is a typed, machine-readable rejection reason.
type ValidationError struct {
	Kind    ErrorKind
	NodeKey string
	Detail  string
}

func (e *ValidationError) Error() string {
	if e.NodeKey != "" {
		return fmt.Sprintf("dag: %s (%s): %s", e.Kind, e.NodeKey, e.Detail)
	}
	return fmt.Sprintf("dag: %s: %s", e.Kind, e.Detail)
}

// Validate checks g against limits and returns the task keys in a legal
// topological order (dependencies before dependents). It is pure: the same
// graph and limits always produce the same order or the same error.
func Validate(g Graph, limits Limits) ([]string, error) {
	nodes := g.Nodes
	if len(nodes) == 0 {
		return nil, &ValidationError{Kind: KindEmpty, Detail: "graph has no tasks"}
	}
	if limits.MaxTasks > 0 && len(nodes) > limits.MaxTasks {
		return nil, &ValidationError{
			Kind:   KindTooMany,
			Detail: fmt.Sprintf("graph has %d tasks, limit is %d", len(nodes), limits.MaxTasks),
		}
	}

	// Index by key, rejecting empty and duplicate keys.
	byKey := make(map[string]Node, len(nodes))
	for i, n := range nodes {
		if n.Key == "" {
			return nil, &ValidationError{
				Kind:   KindEmpty,
				Detail: fmt.Sprintf("task %d has an empty key", i),
			}
		}
		if _, dup := byKey[n.Key]; dup {
			return nil, &ValidationError{Kind: KindDuplicateKey, NodeKey: n.Key, Detail: "duplicate key"}
		}
		byKey[n.Key] = n
	}

	// Parent and dependency references must resolve; neither may be a
	// self-reference.
	for _, n := range nodes {
		if n.ParentKey != "" {
			if n.ParentKey == n.Key {
				return nil, &ValidationError{Kind: KindSelfRef, NodeKey: n.Key, Detail: "task is its own parent"}
			}
			if _, ok := byKey[n.ParentKey]; !ok {
				return nil, &ValidationError{
					Kind:    KindUnknownRef,
					NodeKey: n.Key,
					Detail:  fmt.Sprintf("parent %q does not exist", n.ParentKey),
				}
			}
		}
		for _, dep := range n.DependsOn {
			if dep == n.Key {
				return nil, &ValidationError{Kind: KindSelfRef, NodeKey: n.Key, Detail: "task depends on itself"}
			}
			if _, ok := byKey[dep]; !ok {
				return nil, &ValidationError{
					Kind:    KindUnknownRef,
					NodeKey: n.Key,
					Detail:  fmt.Sprintf("dependency %q does not exist", dep),
				}
			}
		}
	}

	// The parent relation must also be acyclic (a phase ring would break
	// any tree walk, even though it cannot affect scheduling).
	for _, n := range nodes {
		seen := make(map[string]bool)
		for cur := n.Key; cur != ""; {
			if seen[cur] {
				return nil, &ValidationError{Kind: KindCycle, NodeKey: cur, Detail: "parent cycle"}
			}
			seen[cur] = true
			cur = byKey[cur].ParentKey
		}
	}

	// At least one dependency-free root is guaranteed by acyclicity:
	// Kahn's algorithm below reports a cycle if no source exists. The
	// explicit count is therefore unnecessary; the topological pass is
	// the single source of truth for that property.

	order, err := topological(nodes, byKey)
	if err != nil {
		return nil, err
	}

	if err := checkDepth(order, byKey, limits.MaxDepth); err != nil {
		return nil, err
	}
	if err := checkCoverage(nodes, byKey); err != nil {
		return nil, err
	}
	if err := checkBudget(nodes, byKey, limits.MaxTotalJobs); err != nil {
		return nil, err
	}
	return order, nil
}

// topological runs Kahn's algorithm over the dependency edges and returns
// the keys in dependency order. Ties are broken by key so the order is
// deterministic. A graph whose processed count is short of the node count
// contains a dependency cycle.
func topological(nodes []Node, byKey map[string]Node) ([]string, error) {
	indeg := make(map[string]int, len(nodes))
	dependents := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		indeg[n.Key] = len(n.DependsOn)
		for _, dep := range n.DependsOn {
			dependents[dep] = append(dependents[dep], n.Key)
		}
	}
	var ready []string
	for _, n := range nodes {
		if indeg[n.Key] == 0 {
			ready = append(ready, n.Key)
		}
	}
	sort.Strings(ready)

	order := make([]string, 0, len(nodes))
	for len(ready) > 0 {
		key := ready[0]
		ready = ready[1:]
		order = append(order, key)
		next := append([]string(nil), dependents[key]...)
		sort.Strings(next)
		for _, dep := range next {
			indeg[dep]--
			if indeg[dep] == 0 {
				ready = append(ready, dep)
				sort.Strings(ready)
			}
		}
	}
	if len(order) != len(nodes) {
		var remaining []string
		for k, d := range indeg {
			if d > 0 {
				remaining = append(remaining, k)
			}
		}
		sort.Strings(remaining)
		key := ""
		if len(remaining) > 0 {
			key = remaining[0]
		}
		return nil, &ValidationError{Kind: KindCycle, NodeKey: key, Detail: "dependency cycle"}
	}
	return order, nil
}

// checkDepth reports the longest dependency chain exceeding maxDepth
// (a non-positive maxDepth disables the bound). Nodes are visited in
// topological order, so every dependency's depth is known first.
func checkDepth(order []string, byKey map[string]Node, maxDepth int) error {
	if maxDepth <= 0 {
		return nil
	}
	depth := make(map[string]int, len(order))
	for _, key := range order {
		d := 1
		for _, dep := range byKey[key].DependsOn {
			if depth[dep]+1 > d {
				d = depth[dep] + 1
			}
		}
		depth[key] = d
		if d > maxDepth {
			return &ValidationError{
				Kind:    KindDepth,
				NodeKey: key,
				Detail:  fmt.Sprintf("dependency depth %d exceeds limit %d", d, maxDepth),
			}
		}
	}
	return nil
}

// checkCoverage requires every leaf — a task that is nobody's parent — to
// declare at least one acceptance criterion (§7.1).
func checkCoverage(nodes []Node, byKey map[string]Node) error {
	isParent := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if n.ParentKey != "" {
			isParent[n.ParentKey] = true
		}
	}
	for _, n := range nodes {
		if isParent[n.Key] {
			continue
		}
		if len(n.Criteria) == 0 {
			return &ValidationError{Kind: KindCoverage, NodeKey: n.Key, Detail: "leaf task has no acceptance criteria"}
		}
	}
	return nil
}

// checkBudget rejects negative budgets on any task and a leaf-job total
// above maxTotalJobs (a non-positive max disables the total bound).
func checkBudget(nodes []Node, byKey map[string]Node, maxTotalJobs int) error {
	for _, n := range nodes {
		if n.Budget.MaxJobs < 0 || n.Budget.MaxLLMCalls < 0 || n.Budget.MaxTokens < 0 || n.Budget.MaxWallTime < 0 {
			return &ValidationError{Kind: KindBudget, NodeKey: n.Key, Detail: "budget values must not be negative"}
		}
	}
	if maxTotalJobs <= 0 {
		return nil
	}
	isParent := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		if n.ParentKey != "" {
			isParent[n.ParentKey] = true
		}
	}
	total := 0
	for _, n := range nodes {
		if !isParent[n.Key] {
			total += n.Budget.MaxJobs
		}
	}
	if total > maxTotalJobs {
		return &ValidationError{
			Kind:   KindBudget,
			Detail: fmt.Sprintf("leaf job budget %d exceeds limit %d", total, maxTotalJobs),
		}
	}
	return nil
}
