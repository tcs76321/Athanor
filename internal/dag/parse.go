package dag

import (
	"encoding/json"
	"fmt"
	"strings"
)

// wireGraph mirrors the JSON contract the decomposition prompt requests.
type wireGraph struct {
	Tasks []wireNode `json:"tasks"`
}

type wireNode struct {
	Key         string   `json:"key"`
	Parent      string   `json:"parent"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	DependsOn   []string `json:"depends_on"`
	Criteria    []string `json:"acceptance_criteria"`
	Budget      Budget   `json:"budget"`
}

// ParseError reports malformed decomposition output. It is distinct from
// ValidationError: ParseError means "the model did not produce the
// contract"; ValidationError means "the contract was produced but the
// graph is unsound".
type ParseError struct {
	Detail string
}

func (e *ParseError) Error() string { return "dag: " + e.Detail }

// Parse extracts the first JSON object from the model's raw content and
// decodes it into a Graph. It is lenient about code fences and surrounding
// prose (the ADR-0012 brace-scanner precedent) but strict about the
// contract: a non-empty tasks array, and a key and title on every node.
func Parse(raw string) (Graph, error) {
	obj, err := extractObject(raw)
	if err != nil {
		return Graph{}, err
	}
	var w wireGraph
	if err := json.Unmarshal([]byte(obj), &w); err != nil {
		return Graph{}, &ParseError{Detail: fmt.Sprintf("decoding decomposition JSON: %v", err)}
	}
	if len(w.Tasks) == 0 {
		return Graph{}, &ParseError{Detail: "decomposition has no tasks"}
	}
	g := Graph{Nodes: make([]Node, 0, len(w.Tasks))}
	for i, t := range w.Tasks {
		if strings.TrimSpace(t.Key) == "" {
			return Graph{}, &ParseError{Detail: fmt.Sprintf("task %d has no key", i)}
		}
		if strings.TrimSpace(t.Title) == "" {
			return Graph{}, &ParseError{Detail: fmt.Sprintf("task %q has no title", t.Key)}
		}
		g.Nodes = append(g.Nodes, Node{
			Key:         t.Key,
			ParentKey:   t.Parent,
			Title:       t.Title,
			Description: t.Description,
			DependsOn:   t.DependsOn,
			Criteria:    t.Criteria,
			Budget:      t.Budget,
		})
	}
	return g, nil
}

// extractObject returns the first balanced JSON object in content,
// tracking brace depth with quote and escape handling so a '}' inside a
// string does not terminate early.
func extractObject(content string) (string, error) {
	start := strings.IndexByte(content, '{')
	if start < 0 {
		return "", &ParseError{Detail: "no JSON object in decomposition output"}
	}
	depth, end := 0, -1
	inStr, escape := false, false
	for i := start; i < len(content); i++ {
		c := content[i]
		if escape {
			escape = false
			continue
		}
		if c == '\\' && inStr {
			escape = true
			continue
		}
		if c == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return "", &ParseError{Detail: "unterminated JSON object in decomposition output"}
	}
	return content[start : end+1], nil
}
