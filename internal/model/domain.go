package model

import (
	"slices"
	"strings"
)

// DomainDir is where a Container's Domains land: every path a Domain's
// template renders is prefixed with DomainDir + "<domain-name>/".
const DomainDir = "docs/agents/domain/"

// ScaffoldedDomains returns the Domains a Container was scaffolded with,
// sorted: the names of the trailing run of scaffoldedFrom entries (name@ref)
// whose name is none of others, the Container's Framework, Standard, Compute,
// Deployment and Connectors. Only the trailing run counts, since the Domains
// render last; an entry further up that is not in others is a changed choice,
// not a Domain. An adopted Container (no scaffoldedFrom) has none.
func ScaffoldedDomains(scaffoldedFrom, others []string) []string {
	var domains []string
	for _, s := range slices.Backward(scaffoldedFrom) {
		name, _, _ := strings.Cut(s, "@")
		if slices.Contains(others, name) {
			break
		}
		domains = append(domains, name)
	}
	slices.Sort(domains)
	return domains
}

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
