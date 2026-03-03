package runner

import (
	"fmt"
	"sort"
	"strings"
)

// TopoSort takes a flat list of steps and returns them grouped into tiers using
// Kahn's algorithm. Steps within a tier have no dependencies on each other and
// can be executed in parallel. Tiers must be executed sequentially.
//
// Dependencies are resolved by matching Step.Deps file paths against other
// steps' Step.Output paths. Deps that don't match any step's output are
// treated as external files (always satisfied).
//
// Returns an error if a cycle is detected, with a message listing the cycle.
func TopoSort(steps []Step) ([][]Step, error) {
	// Map from output path → step index (for resolving file deps to step deps).
	outputToIdx := make(map[string]int, len(steps))
	for i, s := range steps {
		if s.Output != "" {
			outputToIdx[s.Output] = i
		}
	}

	// Build adjacency list (producer → consumers) and in-degree count.
	// Edge A→B means "B depends on A" (A must run before B).
	inDegree := make([]int, len(steps))
	// successors[i] = list of step indices that depend on step i
	successors := make([][]int, len(steps))

	for i, s := range steps {
		for _, dep := range s.Deps {
			producerIdx, ok := outputToIdx[dep]
			if !ok {
				// External file dependency — no step produces it; always satisfied.
				continue
			}
			if producerIdx == i {
				// A step cannot depend on itself via its own output.
				continue
			}
			successors[producerIdx] = append(successors[producerIdx], i)
			inDegree[i]++
		}
	}

	// Kahn's algorithm: process nodes with in-degree 0 tier by tier.
	var tiers [][]Step
	remaining := len(steps)

	// Seed the first tier with all zero-in-degree steps.
	var current []int
	for i := range steps {
		if inDegree[i] == 0 {
			current = append(current, i)
		}
	}
	// Sort for deterministic output order within each tier.
	sort.Slice(current, func(a, b int) bool {
		return steps[current[a]].ID < steps[current[b]].ID
	})

	for len(current) > 0 {
		tier := make([]Step, len(current))
		for j, idx := range current {
			tier[j] = steps[idx]
		}
		tiers = append(tiers, tier)
		remaining -= len(current)

		// Collect next tier: successors whose in-degree drops to 0.
		var next []int
		for _, idx := range current {
			for _, succ := range successors[idx] {
				inDegree[succ]--
				if inDegree[succ] == 0 {
					next = append(next, succ)
				}
			}
		}
		sort.Slice(next, func(a, b int) bool {
			return steps[next[a]].ID < steps[next[b]].ID
		})
		current = next
	}

	if remaining > 0 {
		// There are nodes still in the graph — they form a cycle.
		return nil, fmt.Errorf("dependency cycle detected among steps: %s",
			describeCycle(steps, inDegree, successors))
	}

	return tiers, nil
}

// describeCycle finds and formats one cycle among the steps that still have
// in-degree > 0 after Kahn's algorithm completes.
func describeCycle(steps []Step, inDegree []int, successors [][]int) string {
	// Find a starting node still in the cycle.
	start := -1
	for i, d := range inDegree {
		if d > 0 {
			start = i
			break
		}
	}
	if start == -1 {
		return "(unknown)"
	}

	// DFS to find a cycle path.
	path := []int{}
	visited := make(map[int]bool)
	onStack := make(map[int]bool)

	var findCycle func(node int) []int
	findCycle = func(node int) []int {
		visited[node] = true
		onStack[node] = true
		path = append(path, node)

		for _, succ := range successors[node] {
			if inDegree[succ] == 0 {
				// This successor was already processed; skip.
				continue
			}
			if onStack[succ] {
				// Found the cycle. Extract the loop portion.
				cycleStart := -1
				for i, n := range path {
					if n == succ {
						cycleStart = i
						break
					}
				}
				return path[cycleStart:]
			}
			if !visited[succ] {
				if cycle := findCycle(succ); cycle != nil {
					return cycle
				}
			}
		}

		path = path[:len(path)-1]
		onStack[node] = false
		return nil
	}

	cycle := findCycle(start)
	if cycle == nil {
		// Fallback: just list the IDs of cyclic steps.
		var ids []string
		for i, d := range inDegree {
			if d > 0 {
				ids = append(ids, steps[i].ID)
			}
		}
		return strings.Join(ids, " → ")
	}

	parts := make([]string, len(cycle))
	for i, idx := range cycle {
		parts[i] = steps[idx].ID
	}
	// Close the loop visually.
	return strings.Join(parts, " → ") + " → " + steps[cycle[0]].ID
}
