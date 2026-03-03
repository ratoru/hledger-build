package runner

import (
	"strings"
	"testing"
)

// helper: build a simple Step with just ID, Output, and Deps.
func makeStep(id, output string, deps ...string) Step {
	return Step{
		ID:      id,
		Output:  output,
		Deps:    deps,
		Command: "true",
	}
}

// flatIDs flattens a [][]Step into a slice of ID slices for easy comparison.
func tierIDs(tiers [][]Step) [][]string {
	out := make([][]string, len(tiers))
	for i, tier := range tiers {
		ids := make([]string, len(tier))
		for j, s := range tier {
			ids[j] = s.ID
		}
		out[i] = ids
	}
	return out
}

// containsAll checks that every expected string is in haystack.
func containsAll(haystack string, needles ...string) bool {
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			return false
		}
	}
	return true
}

func TestTopoSort_EmptyInput(t *testing.T) {
	tiers, err := TopoSort(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tiers) != 0 {
		t.Fatalf("expected 0 tiers, got %d", len(tiers))
	}
}

func TestTopoSort_SingleNode(t *testing.T) {
	steps := []Step{makeStep("a", "out/a.txt")}
	tiers, err := TopoSort(steps)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tiers) != 1 {
		t.Fatalf("expected 1 tier, got %d", len(tiers))
	}
	if len(tiers[0]) != 1 || tiers[0][0].ID != "a" {
		t.Fatalf("expected tier[0] = [a], got %v", tierIDs(tiers))
	}
}

func TestTopoSort_LinearChain(t *testing.T) {
	// a → b → c  (each step depends on the previous)
	a := makeStep("a", "out/a.txt")
	b := makeStep("b", "out/b.txt", "out/a.txt")
	c := makeStep("c", "out/c.txt", "out/b.txt")

	tiers, err := TopoSort([]Step{a, b, c})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tiers) != 3 {
		t.Fatalf("expected 3 tiers, got %d: %v", len(tiers), tierIDs(tiers))
	}
	if tiers[0][0].ID != "a" || tiers[1][0].ID != "b" || tiers[2][0].ID != "c" {
		t.Fatalf("unexpected tier order: %v", tierIDs(tiers))
	}
}

func TestTopoSort_Diamond(t *testing.T) {
	// root → left, root → right, left + right → sink
	root := makeStep("root", "out/root.txt")
	left := makeStep("left", "out/left.txt", "out/root.txt")
	right := makeStep("right", "out/right.txt", "out/root.txt")
	sink := makeStep("sink", "out/sink.txt", "out/left.txt", "out/right.txt")

	tiers, err := TopoSort([]Step{sink, right, left, root}) // deliberately shuffled
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tiers) != 3 {
		t.Fatalf("expected 3 tiers, got %d: %v", len(tiers), tierIDs(tiers))
	}
	// Tier 0: root; Tier 1: left, right (parallel); Tier 2: sink.
	if len(tiers[0]) != 1 || tiers[0][0].ID != "root" {
		t.Fatalf("tier 0 should be [root], got %v", tierIDs(tiers)[0])
	}
	if len(tiers[1]) != 2 {
		t.Fatalf("tier 1 should have 2 steps, got %v", tierIDs(tiers)[1])
	}
	if len(tiers[2]) != 1 || tiers[2][0].ID != "sink" {
		t.Fatalf("tier 2 should be [sink], got %v", tierIDs(tiers)[2])
	}
}

func TestTopoSort_MultipleTiers(t *testing.T) {
	// Tier 0: a, b (no deps)
	// Tier 1: c (dep a), d (dep b)
	// Tier 2: e (dep c and d)
	a := makeStep("a", "a.txt")
	b := makeStep("b", "b.txt")
	c := makeStep("c", "c.txt", "a.txt")
	d := makeStep("d", "d.txt", "b.txt")
	e := makeStep("e", "e.txt", "c.txt", "d.txt")

	tiers, err := TopoSort([]Step{e, d, c, b, a})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tiers) != 3 {
		t.Fatalf("expected 3 tiers, got %d", len(tiers))
	}
	if len(tiers[0]) != 2 {
		t.Fatalf("tier 0 should have 2 steps, got %v", tierIDs(tiers)[0])
	}
	if len(tiers[1]) != 2 {
		t.Fatalf("tier 1 should have 2 steps, got %v", tierIDs(tiers)[1])
	}
	if len(tiers[2]) != 1 || tiers[2][0].ID != "e" {
		t.Fatalf("tier 2 should be [e], got %v", tierIDs(tiers)[2])
	}
}

func TestTopoSort_ExternalDepsIgnored(t *testing.T) {
	// Deps that don't match any step output are external files (always satisfied).
	// Both steps should land in tier 0.
	a := makeStep("a", "a.txt", "external/source.csv")
	b := makeStep("b", "b.txt", "external/rules.rules")

	tiers, err := TopoSort([]Step{a, b})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tiers) != 1 {
		t.Fatalf("expected 1 tier (both independent), got %d: %v", len(tiers), tierIDs(tiers))
	}
	if len(tiers[0]) != 2 {
		t.Fatalf("expected 2 steps in tier 0, got %v", tierIDs(tiers)[0])
	}
}

func TestTopoSort_CycleDetected_Simple(t *testing.T) {
	// a → b → a  (simple 2-node cycle)
	a := makeStep("a", "a.txt", "b.txt")
	b := makeStep("b", "b.txt", "a.txt")

	_, err := TopoSort([]Step{a, b})
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error should mention 'cycle', got: %v", err)
	}
	// Both step IDs should appear in the error.
	if !containsAll(err.Error(), "a", "b") {
		t.Fatalf("cycle error should name the cyclic steps, got: %v", err)
	}
}

func TestTopoSort_CycleDetected_Triangle(t *testing.T) {
	// a → b → c → a
	a := makeStep("a", "a.txt", "c.txt")
	b := makeStep("b", "b.txt", "a.txt")
	c := makeStep("c", "c.txt", "b.txt")

	_, err := TopoSort([]Step{a, b, c})
	if err == nil {
		t.Fatal("expected cycle error, got nil")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error should mention 'cycle', got: %v", err)
	}
}

func TestTopoSort_DeterministicOrder(t *testing.T) {
	// Two independent steps should always appear in the same (sorted) order.
	a := makeStep("a", "a.txt")
	b := makeStep("b", "b.txt")

	for i := 0; i < 5; i++ {
		tiers, err := TopoSort([]Step{b, a}) // shuffled input
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		ids := tierIDs(tiers)
		if ids[0][0] != "a" || ids[0][1] != "b" {
			t.Fatalf("iteration %d: expected [a b], got %v", i, ids[0])
		}
	}
}

func TestTopoSort_StepWithNoOutput(t *testing.T) {
	// A step with an empty Output should still be sortable; it won't be a dep producer.
	a := makeStep("a", "")
	b := makeStep("b", "b.txt")

	tiers, err := TopoSort([]Step{a, b})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both are independent (a produces nothing, b doesn't dep on a).
	if len(tiers) != 1 || len(tiers[0]) != 2 {
		t.Fatalf("expected 1 tier with 2 steps, got %v", tierIDs(tiers))
	}
}

func TestTopoSort_MixedDeps(t *testing.T) {
	// c depends on a (step-produced) and an external file.
	// c must still come after a.
	a := makeStep("a", "a.txt")
	b := makeStep("b", "b.txt") // independent
	c := makeStep("c", "c.txt", "a.txt", "external.csv")

	tiers, err := TopoSort([]Step{c, b, a})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Tier 0: a, b; Tier 1: c
	if len(tiers) != 2 {
		t.Fatalf("expected 2 tiers, got %d: %v", len(tiers), tierIDs(tiers))
	}
	if len(tiers[0]) != 2 {
		t.Fatalf("tier 0 should have a and b, got %v", tierIDs(tiers)[0])
	}
	if len(tiers[1]) != 1 || tiers[1][0].ID != "c" {
		t.Fatalf("tier 1 should be [c], got %v", tierIDs(tiers)[1])
	}
}
