package model

import (
	"fmt"
	"slices"
)

// Bundle is a catalog bundle: the names of a Framework and, optionally, a
// Standard, a Compute and a Deployment a team picked together
// (docs/plans/catalog-bundles.md). It never names a Connector.
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
