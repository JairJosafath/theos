package model

import "slices"

// DomainDir is where a Container's Domains land: every path a Domain's
// template renders is prefixed with DomainDir + "<domain-name>/".
const DomainDir = "docs/agents/domain/"

// DomainsDiff compares the Domains a Container was scaffolded with against the
// ones its system names now: added are in wanted and not in have, removed in
// have and not in wanted, both sorted and unique.
func DomainsDiff(have, wanted []string) (added, removed []string) {
	have = sortedUnique(have)
	wanted = sortedUnique(wanted)
	for _, d := range wanted {
		if !slices.Contains(have, d) {
			added = append(added, d)
		}
	}
	for _, d := range have {
		if !slices.Contains(wanted, d) {
			removed = append(removed, d)
		}
	}
	return added, removed
}

func sortedUnique(names []string) []string {
	out := slices.Clone(names)
	slices.Sort(out)
	return slices.Compact(out)
}
