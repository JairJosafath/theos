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
	"fmt"
	"slices"
	"strings"
)

// Bundle is a catalog bundle: the names of a Framework and, optionally, a
// Standard, a Compute and a Deployment a team picked together
// (docs/plans/done/catalog-bundles.md). It never names a Connector.
type Bundle struct {
	Name        string
	Description string
	Framework   string
	Standard    string
	Compute     string
	Deployment  string
}

// BundleChoice is one side a bundle names: the catalog side and the entry's name.
type BundleChoice struct {
	Side string
	Name string
}

// BundleChoices expands a bundle to the entries it names, in the order their
// layers are rendered, leaving out the optional sides it doesn't name.
func BundleChoices(b Bundle) []BundleChoice {
	var out []BundleChoice
	for _, c := range []BundleChoice{
		{SideFramework, b.Framework},
		{SideStandard, b.Standard},
		{SideCompute, b.Compute},
		{SideDeployment, b.Deployment},
	} {
		if c.Name != "" {
			out = append(out, c)
		}
	}
	return out
}

// BundleSupportedTypes is what a bundle supports: the Container types every one
// of its entries lists, in the order the first entry lists them. Entries that
// share no type, or no entries at all, give none.
func BundleSupportedTypes(entries []CatalogEntry) []string {
	if len(entries) == 0 {
		return nil
	}
	var out []string
	for _, t := range entries[0].SupportedTypes {
		if slices.Contains(out, t) {
			continue
		}
		if !slices.ContainsFunc(entries[1:], func(e CatalogEntry) bool { return !e.Supports(t) }) {
			out = append(out, t)
		}
	}
	return out
}

// BundleEntries expands a bundle to the catalog entries it names, in the
// order their layers are rendered: the same expansion the operator makes
// before it checks a Container's choices. The first missing entry is named,
// as "Framework go-service", and ends the expansion.
func BundleEntries(b Bundle, catalog []CatalogEntry) (entries []CatalogEntry, missing string) {
	for _, c := range BundleChoices(b) {
		i := slices.IndexFunc(catalog, func(e CatalogEntry) bool { return e.Side == c.Side && e.Name == c.Name })
		if i < 0 {
			return nil, c.Side + " " + c.Name
		}
		entries = append(entries, catalog[i])
	}
	return entries, ""
}

// CheckBundle checks a bundle's entries for a Container type as the operator
// does (CheckChoices, CheckStandard and CheckDeploysItself), with the error
// naming the bundle (InBundle).
func CheckBundle(b Bundle, containerType string, entries []CatalogEntry) error {
	err := CheckChoices(containerType, entries)
	if err == nil {
		var standard *CatalogEntry
		if i := slices.IndexFunc(entries, func(e CatalogEntry) bool { return e.Side == SideStandard }); i >= 0 {
			standard = &entries[i]
		}
		err = CheckStandard(b.Framework, standard)
	}
	if err == nil && len(entries) > 0 {
		err = CheckDeploysItself(entries[0], containerType, b.Compute, b.Deployment)
	}
	return InBundle(b.Name, err)
}

// InBundle names the bundle a Container's choices came from at the start of a
// choice check's error, as "bundle web-service: ...", so the message says the
// fix is to the Bundle. The error stays what it was underneath; without a
// bundle, or without an error, it is returned as is.
func InBundle(bundle string, err error) error {
	if bundle == "" || err == nil {
		return err
	}
	return fmt.Errorf("bundle %s: %w", bundle, err)
}

// BundleChanges lists the sides now names differently from scaffolded, the
// expansion a Container scaffolded from, in side order, each as
// "standard: python-standard, now python-standard-2" with "none" for a side a
// bundle doesn't name. A changed Bundle is reported, never applied
// (docs/plans/done/catalog-bundles.md, decision 6).
func BundleChanges(scaffolded, now Bundle) []string {
	var out []string
	for _, s := range []struct{ side, was, is string }{
		{SideFramework, scaffolded.Framework, now.Framework},
		{SideStandard, scaffolded.Standard, now.Standard},
		{SideCompute, scaffolded.Compute, now.Compute},
		{SideDeployment, scaffolded.Deployment, now.Deployment},
	} {
		if s.was != s.is {
			out = append(out, fmt.Sprintf("%s: %s, now %s", strings.ToLower(s.side), orNone(s.was), orNone(s.is)))
		}
	}
	return out
}

// none is how a side a Bundle doesn't name reads in a message.
const none = "none"

func orNone(name string) string {
	if name == "" {
		return none
	}
	return name
}

// String is the bundle as the scaffold's commit records it: its name, then
// each side it names as side=entry, in side order. ParseBundle reads it back.
func (b Bundle) String() string {
	choices := BundleChoices(b)
	parts := make([]string, 0, 1+len(choices))
	parts = append(parts, b.Name)
	for _, c := range choices {
		parts = append(parts, strings.ToLower(c.Side)+"="+c.Name)
	}
	return strings.Join(parts, " ")
}

// ParseBundle reads a bundle String wrote. A side it doesn't know is ignored.
func ParseBundle(s string) Bundle {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return Bundle{}
	}
	b := Bundle{Name: fields[0]}
	for _, f := range fields[1:] {
		side, name, _ := strings.Cut(f, "=")
		switch side {
		case strings.ToLower(SideFramework):
			b.Framework = name
		case strings.ToLower(SideStandard):
			b.Standard = name
		case strings.ToLower(SideCompute):
			b.Compute = name
		case strings.ToLower(SideDeployment):
			b.Deployment = name
		}
	}
	return b
}
