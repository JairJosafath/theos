package model

import (
	"fmt"
	"slices"
)

// Bundle is a catalog bundle: the names of a Framework and, optionally, a
// Standard, a Compute and a Deployment a team picked together
// (docs/plans/catalog-bundles.md). It never names a Connector.
type Bundle struct {
	Name       string
	Framework  string
	Standard   string
	Compute    string
	Deployment string
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
