/*
Copyright 2026 The theos Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package model

import (
	"slices"
	"strings"
)

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

// AdoptionDomains are the sets of Domains an adoption renders the Standard
// with, to try in order (#42): the Domains the identity file's scaffoldedFrom
// records, its entries isDomain says are a Domain of the catalog; the
// Domains the system names now; and none, as for a repository theos never
// scaffolded. Each set is sorted and unique, as the scaffold passes them; an
// empty set or a repeat of an earlier one is left out, and none always ends
// the list. scaffoldedFrom tells a Domain from a Connector only by its name,
// so a name both are is tried as a Domain first and the next set after it.
func AdoptionDomains(scaffoldedFrom []string, isDomain func(string) bool, systemDomains []string) [][]string {
	var recorded []string
	for _, pinned := range scaffoldedFrom {
		if name, _, _ := strings.Cut(pinned, "@"); isDomain(name) {
			recorded = append(recorded, name)
		}
	}
	var sets [][]string
	for _, set := range [][]string{sortedUnique(recorded), sortedUnique(systemDomains)} {
		if len(set) > 0 && !slices.ContainsFunc(sets, func(s []string) bool { return slices.Equal(s, set) }) {
			sets = append(sets, set)
		}
	}
	return append(sets, nil)
}
