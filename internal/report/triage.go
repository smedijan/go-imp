package report

import (
	"crypto/sha256"
	"fmt"
	"slices"

	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report/internal"
)

func reviewGroups(mutants []mutator.Mutator) []internal.ReviewGroup {
	// Lexically sorted IDs, rather than positions, keep chunks stable on line shifts.
	buckets := make(map[string][]string)
	counts := make(map[string]int)
	for _, m := range mutants {
		if identified, ok := m.(mutator.Identified); ok {
			counts[identified.Identity().ID]++
		}
	}
	for _, m := range mutants {
		identified, ok := m.(mutator.Identified)
		if !ok || identified.Identity().ID == "" || counts[identified.Identity().ID] != 1 {
			continue
		}
		provider, ok := m.(mutator.ObservationProvider)
		if !ok {
			continue
		}
		c := provider.ObservationContext()
		if c == nil {
			continue
		}
		if !c.PackagePath.Known || c.PackagePath.Truncated || !c.EnclosingSymbol.Known || c.EnclosingSymbol.Truncated {
			continue
		}
		key := fmt.Sprintf("same file=%q package=%q owner=%q status=%q (heuristic, not equivalence)", identified.Identity().ModulePath, c.PackagePath.Value, c.EnclosingSymbol.Value, m.Status())
		buckets[key] = append(buckets[key], identified.Identity().ID)
	}
	keys := make([]string, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	var groups []internal.ReviewGroup
	for _, key := range keys {
		ids := buckets[key]
		slices.Sort(ids)
		for start := 0; start < len(ids); start += 10 {
			end := min(start+10, len(ids))
			members := ids[start:end]
			digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%v", key, members)))
			groups = append(groups, internal.ReviewGroup{ID: fmt.Sprintf("review-v1:%x", digest), Criterion: key, MutantIDs: members})
		}
	}

	return groups
}
