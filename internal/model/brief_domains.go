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
)

// A Brief's planner picks a new system's Domains (docs/plans/domains.md,
// decision 9, #1760): a SoftwareSystem's domainRefs name the installed
// Domains the idea is about, from model/catalog.yaml's `domains`. The round
// that creates the system sets them, and every Container of the system is
// scaffolded with them; a later round changes nothing, so a later Domain is a
// prompt for a new system, or a person's edit.

// installedDomains are the names of the catalog's Domains.
func installedDomains(catalog []CatalogEntry) []string {
	var out []string
	for _, e := range catalog {
		if e.Side == SideDomain {
			out = append(out, e.Name)
		}
	}
	slices.Sort(out)
	return out
}

// sortedDomains is domains sorted, for a compare that ignores the order.
func sortedDomains(domains []string) []string {
	out := slices.Clone(domains)
	slices.Sort(out)
	return out
}

// checkDomains checks the plan's SoftwareSystems' domainRefs: a created system
// names installed Domains, each once, and none when it is External (the CRD
// refuses that too); an existing system keeps the Domains it has.
func checkDomains(plan Plan, current CurrentModel, refuse func(string, string, ...any)) {
	installed := installedDomains(current.Catalog)
	for _, o := range plan.Objects {
		if o.Kind != KindSoftwareSystem {
			continue
		}
		subject := o.Ref().String()
		if before, exists := findObject(current.Objects, o.Ref()); exists {
			if was, is := sortedDomains(before.Domains), sortedDomains(o.Domains); !slices.Equal(was, is) {
				refuse(subject, "domainRefs can't change (from %v to %v): a system's Domains are picked when it "+
					"is made; a later Domain is a prompt for a new system, or a person's edit", was, is)
			}
			continue
		}
		if len(o.Domains) == 0 {
			continue
		}
		if SystemLocation(o) == LocationExternal {
			refuse(subject, "an External system names no domainRefs: the factory builds nothing there")
			continue
		}
		seen := map[string]bool{}
		for _, name := range o.Domains {
			if seen[name] {
				refuse(subject, "domainRefs names %q twice; name each Domain once", name)
				continue
			}
			seen[name] = true
			if !slices.Contains(installed, name) {
				refuse(subject, "domainRefs names %q, which is not an installed Domain of %s; name only its "+
					"`domains`, or none", name, WorkspaceCatalogFile)
			}
		}
	}
}
