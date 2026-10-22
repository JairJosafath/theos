package model

import (
	"fmt"
	"slices"
	"strings"
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

// BundleChanges lists the sides now names differently from scaffolded, the
// expansion a Container scaffolded from, in side order, each as
// "standard: python-standard, now python-standard-2" with "none" for a side a
// bundle doesn't name. A changed Bundle is reported, never applied
// (docs/plans/catalog-bundles.md, decision 6).
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

func orNone(name string) string {
	if name == "" {
		return "none"
	}
	return name
}

// String is the bundle as the scaffold's commit records it: its name, then
// each side it names as side=entry, in side order. ParseBundle reads it back.
func (b Bundle) String() string {
	parts := []string{b.Name}
	for _, c := range BundleChoices(b) {
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
