package dag

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// TestValidateProperty generates acyclic graphs from a forward-only edge
// rule (so they are guaranteed valid), asserts Validate accepts them and
// returns a dependency-respecting order, then adds a back-edge to create a
// cycle and asserts the same graph is rejected as a cycle.
func TestValidateProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		n := 2 + rng.Intn(8)
		nodes := make([]Node, n)
		for i := range nodes {
			nodes[i] = Node{Key: fmt.Sprintf("t%d", i), Title: "t", Criteria: []string{"c"}}
			if i > 0 {
				// The linear chain keeps every node reachable so the
				// back-edge below always closes a cycle.
				nodes[i].DependsOn = append(nodes[i].DependsOn, nodes[i-1].Key)
				for j := 0; j < i-1; j++ {
					if rng.Intn(3) == 0 {
						nodes[i].DependsOn = append(nodes[i].DependsOn, nodes[j].Key)
					}
				}
			}
		}
		order, err := Validate(Graph{Nodes: nodes}, Limits{})
		if err != nil {
			t.Fatalf("iter %d: valid DAG rejected: %v", iter, err)
		}
		assertOrderRespectsDeps(t, nodes, order)

		cyc := append([]Node(nil), nodes...)
		cyc[0].DependsOn = append(cyc[0].DependsOn, cyc[n-1].Key)
		_, err = Validate(Graph{Nodes: cyc}, Limits{})
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Kind != KindCycle {
			t.Fatalf("iter %d: cycle not rejected as KindCycle: %v", iter, err)
		}
	}
}

func assertOrderRespectsDeps(t *testing.T, nodes []Node, order []string) {
	t.Helper()
	if len(order) != len(nodes) {
		t.Fatalf("order length %d != node count %d", len(order), len(nodes))
	}
	pos := make(map[string]int, len(order))
	for i, k := range order {
		pos[k] = i
	}
	for _, n := range nodes {
		for _, dep := range n.DependsOn {
			if pos[dep] >= pos[n.Key] {
				t.Fatalf("order violates %s -> %s: %v", dep, n.Key, order)
			}
		}
	}
}
