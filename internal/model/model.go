// Package model holds the business rules of the C4 story as plain functions over
// plain values: which catalog choices fit a Container, which Connectors a Container
// picks up through its Relationships, and how status rolls up.
//
// It must not import Kubernetes, controller-runtime, net/http or the api package;
// see docs/architecture.md. Controllers convert resources into these values.
package model

import (
	"fmt"
	"slices"
	"strings"
)

// Container types, mirroring the fixed list in the API.
const (
	TypeStaticSite = "static-site"
	TypeService    = "service"
	TypeJob        = "job"
	TypeWorker     = "worker"
)

// Catalog sides, in the order their layers are rendered.
const (
	SideFramework  = "Framework"
	SideCompute    = "Compute"
	SideDeployment = "Deployment"
	SideConnector  = "Connector"
)

// CatalogEntry is one side of a Container: a pinned template plus the Container
// types it supports.
type CatalogEntry struct {
	Side           string
	Name           string
	SupportedTypes []string
	TemplateURL    string
	TemplateRef    string
}

// Supports reports whether the entry lists the Container type.
func (e CatalogEntry) Supports(containerType string) bool {
	return slices.Contains(e.SupportedTypes, containerType)
}

// Pinned returns the entry as name@ref, the form recorded in scaffoldedFrom.
func (e CatalogEntry) Pinned() string {
	return e.Name + "@" + e.TemplateRef
}

// ChoiceError lists every catalog entry that doesn't support the Container type.
type ChoiceError struct {
	Type        string
	Unsupported []CatalogEntry
}

func (e *ChoiceError) Error() string {
	names := make([]string, 0, len(e.Unsupported))
	for _, entry := range e.Unsupported {
		names = append(names, entry.Name)
	}
	verb := "does"
	if len(names) > 1 {
		verb = "do"
	}
	return fmt.Sprintf("%s %s not support %s", joinAnd(names), verb, e.Type)
}

// CheckChoices returns a *ChoiceError when any entry, including Connectors reached
// through Relationships, doesn't support the Container type.
func CheckChoices(containerType string, entries []CatalogEntry) error {
	var unsupported []CatalogEntry
	for _, entry := range entries {
		if !entry.Supports(containerType) {
			unsupported = append(unsupported, entry)
		}
	}
	if len(unsupported) == 0 {
		return nil
	}
	return &ChoiceError{Type: containerType, Unsupported: unsupported}
}

// Element kinds a Relationship can connect, mirroring the API.
const (
	KindPerson         = "Person"
	KindSoftwareSystem = "SoftwareSystem"
	KindContainer      = "Container"
	KindDataStore      = "DataStore"
)

// Ref names a story element.
type Ref struct {
	Kind string
	Name string
}

func (r Ref) String() string { return r.Kind + "/" + r.Name }

// Relationship is a directed link between two elements.
type Relationship struct {
	Name   string
	Source Ref
	Target Ref
}

// ChangesScaffold reports whether the link adds anything to a scaffold. In v1 only
// a Container to DataStore link does: it applies the DataStore's Connector.
func (r Relationship) ChangesScaffold() bool {
	return r.Source.Kind == KindContainer && r.Target.Kind == KindDataStore
}

// DataStoreUse is what a Container picks up from its Relationships to DataStores.
type DataStoreUse struct {
	// Connectors are the unique Connector names, sorted. A Connector reached through
	// two DataStores is applied once.
	Connectors []string
	// Relationships are the names of the Relationships that were applied, sorted.
	Relationships []string
	// MissingDataStores are DataStores named by a Relationship that don't exist.
	MissingDataStores []string
}

// DataStoresUsedBy works out which Connectors a Container picks up. connectorOf maps
// a DataStore name to its Connector name, for the DataStores that exist.
func DataStoresUsedBy(container string, rels []Relationship, connectorOf map[string]string) DataStoreUse {
	var use DataStoreUse
	for _, rel := range rels {
		if !rel.ChangesScaffold() || rel.Source.Name != container {
			continue
		}
		connector, ok := connectorOf[rel.Target.Name]
		if !ok {
			use.MissingDataStores = append(use.MissingDataStores, rel.Target.Name)
			continue
		}
		use.Relationships = append(use.Relationships, rel.Name)
		if !slices.Contains(use.Connectors, connector) {
			use.Connectors = append(use.Connectors, connector)
		}
	}
	slices.Sort(use.Connectors)
	slices.Sort(use.Relationships)
	slices.Sort(use.MissingDataStores)
	use.MissingDataStores = slices.Compact(use.MissingDataStores)
	return use
}

// Summary is the one-line roll-up shown on a SoftwareSystem and a Project, such as
// "3 of 4 containers Ready, 2 active sessions".
func Summary(ready, total, sessions int) string {
	return fmt.Sprintf("%d of %d %s Ready, %d active %s",
		ready, total, plural(total, "container", "containers"),
		sessions, plural(sessions, "session", "sessions"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}
