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
	TypeMobileApp  = "mobile-app"
	TypeCLI        = "cli"
	TypeExtension  = "extension"
	TypeOperator   = "operator"
	TypeFunction   = "function"
)

// Catalog sides, in the order their layers are rendered.
const (
	SideFramework  = "Framework"
	SideStandard   = "Standard"
	SideCompute    = "Compute"
	SideDeployment = "Deployment"
	SideConnector  = "Connector"
)

// CatalogEntry is one side of a Container: a pinned template plus the Container
// types it supports.
type CatalogEntry struct {
	Side           string
	Name           string
	Description    string
	SupportedTypes []string
	TemplateURL    string
	TemplateRef    string
	// SupportedFrameworks lists the Frameworks a Standard fits; empty for other sides.
	SupportedFrameworks []string
	// ChecksBeforeRelease says a Standard can run the full check only before a
	// release; false for other sides.
	ChecksBeforeRelease bool
	// DeploysItself says a Framework's template is its Container's deployment;
	// false for other sides.
	DeploysItself bool
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

// FitError says a Standard does not fit the Container's Framework.
type FitError struct {
	Standard  string
	Framework string
}

func (e *FitError) Error() string {
	return fmt.Sprintf("%s does not support %s", e.Standard, e.Framework)
}

// CheckStandard returns a *FitError when a Standard does not list the Container's
// Framework. A Container without a Standard fits.
func CheckStandard(framework string, standard *CatalogEntry) error {
	if standard == nil || slices.Contains(standard.SupportedFrameworks, framework) {
		return nil
	}
	return &FitError{Standard: standard.Name, Framework: framework}
}

// SelfDeployError says a Container names a Compute or Deployment while its
// Framework deploys itself.
type SelfDeployError struct {
	Framework string
	// Refs lists the Container's computeRef and deploymentRef that are set, as
	// "computeRef small".
	Refs []string
}

func (e *SelfDeployError) Error() string {
	return fmt.Sprintf("%s deploys itself: its template's app (on AWS, its CDK app) is the Container's deployment, "+
		"so it takes no Compute and no Deployment; remove %s", e.Framework, joinAnd(e.Refs))
}

// CheckDeploysItself returns a *SelfDeployError when the Framework deploys
// itself and the Container names a Compute or a Deployment, each "" when unset.
func CheckDeploysItself(framework CatalogEntry, compute, deployment string) error {
	if !framework.DeploysItself {
		return nil
	}
	var refs []string
	if compute != "" {
		refs = append(refs, "computeRef "+compute)
	}
	if deployment != "" {
		refs = append(refs, "deploymentRef "+deployment)
	}
	if len(refs) == 0 {
		return nil
	}
	return &SelfDeployError{Framework: framework.Name, Refs: refs}
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

// AppliedRelationship is a Relationship whose Connector went into a scaffold: the
// DataStore it pointed at and the Connector that DataStore used. A record from
// before targets were kept has only a Name.
type AppliedRelationship struct {
	Name      string
	DataStore string
	Connector string
}

// String returns the form kept in a commit trailer, such as
// "orders-api-to-orders-db DataStore/orders-db Connector/postgres".
func (a AppliedRelationship) String() string {
	if a.DataStore == "" {
		return a.Name
	}
	return fmt.Sprintf("%s %s/%s Connector/%s", a.Name, KindDataStore, a.DataStore, a.Connector)
}

// ParseAppliedRelationship reads the String form, and the older name-only form.
func ParseAppliedRelationship(s string) AppliedRelationship {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return AppliedRelationship{}
	}
	a := AppliedRelationship{Name: fields[0]}
	for _, f := range fields[1:] {
		kind, name, _ := strings.Cut(f, "/")
		switch kind {
		case KindDataStore:
			a.DataStore = name
		case "Connector":
			a.Connector = name
		}
	}
	return a
}

// StillApplied reports whether a Relationship that now points at dataStore, whose
// Connector is connector, is the one recorded as applied: one recreated under the
// same name with another target is not. A name-only record can't tell, so it
// matches. An empty connector means the DataStore is gone; it isn't compared.
func (a AppliedRelationship) StillApplied(dataStore, connector string) bool {
	if a.DataStore == "" {
		return true
	}
	return a.DataStore == dataStore && (connector == "" || a.Connector == connector)
}

// Applied is whether a Relationship's Connector went into its source Container's
// scaffold.
type Applied int

const (
	// AppliedWaiting means the Container isn't scaffolded yet.
	AppliedWaiting Applied = iota
	// AppliedAtScaffold means the scaffold included the Connector.
	AppliedAtScaffold
	// AddedAfterScaffold means the Container was scaffolded without it: the link
	// came later, or was recreated under the same name with another target.
	// Scaffolding happens once.
	AddedAfterScaffold
)

// ScaffoldedRelationship is what a Relationship's Applied condition depends on.
type ScaffoldedRelationship struct {
	// Name is the Relationship's name.
	Name string
	// ContainerReady is whether the source Container exists and is scaffolded.
	ContainerReady bool
	// AppliedNames are the Relationships the Container recorded as applied.
	AppliedNames []string
	// AppliedRecords are the Container's records with targets; a Container
	// scaffolded before targets were recorded has none.
	AppliedRecords []AppliedRelationship
	// DataStore is the Relationship's current target.
	DataStore string
	// Connector is that DataStore's Connector, empty when the DataStore is gone.
	Connector string
}

// RelationshipApplied decides a Relationship's Applied condition.
func RelationshipApplied(r ScaffoldedRelationship) Applied {
	if !r.ContainerReady {
		return AppliedWaiting
	}
	if !slices.Contains(r.AppliedNames, r.Name) {
		return AddedAfterScaffold
	}
	record := AppliedRelationship{Name: r.Name}
	for _, a := range r.AppliedRecords {
		if a.Name == r.Name {
			record = a
		}
	}
	if !record.StillApplied(r.DataStore, r.Connector) {
		return AddedAfterScaffold
	}
	return AppliedAtScaffold
}

// ExistingHistory is what a scaffold does with a repository that already has
// commits.
type ExistingHistory int

const (
	// HistoryEmpty means the repository is new: scaffold it.
	HistoryEmpty ExistingHistory = iota
	// HistoryRecover means the only commit is this Container's own initial
	// commit, pushed by an earlier attempt that didn't get to record it.
	HistoryRecover
	// HistoryForeign means commits the factory didn't make: it only scaffolds
	// into a new, empty repository.
	HistoryForeign
)

// ScaffoldHistory decides what to do with a repository's history before
// scaffolding container (project/container). commits is how many commits the
// repository has, counted up to two; initialFor is the Container the newest
// commit's trailers name, empty when the factory didn't make it.
func ScaffoldHistory(commits int, initialFor, container string) ExistingHistory {
	switch {
	case commits == 0:
		return HistoryEmpty
	case commits == 1 && initialFor != "" && initialFor == container:
		return HistoryRecover
	default:
		return HistoryForeign
	}
}

// DataStoreUse is what a Container picks up from its Relationships to DataStores.
type DataStoreUse struct {
	// Connectors are the unique Connector names, sorted. A Connector reached through
	// two DataStores is applied once.
	Connectors []string
	// Relationships are the Relationships that were applied, sorted by name.
	Relationships []AppliedRelationship
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
		use.Relationships = append(use.Relationships,
			AppliedRelationship{Name: rel.Name, DataStore: rel.Target.Name, Connector: connector})
		if !slices.Contains(use.Connectors, connector) {
			use.Connectors = append(use.Connectors, connector)
		}
	}
	slices.Sort(use.Connectors)
	slices.SortFunc(use.Relationships, func(a, b AppliedRelationship) int { return strings.Compare(a.Name, b.Name) })
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

// checkSuccess is the state of a check, or of all of a commit's checks, that passed.
const checkSuccess = "success"

// checkSkipped is the state of a check that did not run, such as a workflow's
// push run off main.
const checkSkipped = "skipped"

// checkCancelled is the conclusion of a CI run that was cancelled; Gitea
// reports such a run's check as failed.
const checkCancelled = "cancelled"

// checkFailure and checkError are the states of a commit's checks that failed.
const (
	checkFailure = "failure"
	checkError   = "error"
)

// checksFailed reports whether a commit's checks, as a whole, failed.
func checksFailed(state string) bool { return state == checkFailure || state == checkError }

// UnpassedChecks returns the patterns of required, a CI system's
// CheckContexts, that contexts, the state of each check on a commit by its
// context, does not pass, the way branch protection
// reads them: no check that ran matches the pattern, or one that matches has not
// succeeded. A skipped check neither passes nor fails a pattern.
func UnpassedChecks(required []string, contexts map[string]string) []string {
	var out []string
	for _, pattern := range required {
		matched, passed := false, true
		for context, state := range contexts {
			if state != checkSkipped && matchGlob(pattern, context) {
				matched, passed = true, passed && state == checkSuccess
			}
		}
		if !matched || !passed {
			out = append(out, pattern)
		}
	}
	return out
}

// matchGlob reports whether s matches pattern, in which * stands for any run of
// characters, slashes included, as in the Git host's status check patterns.
func matchGlob(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// Protected reports whether a Container's default branch is protected by required
// checks: only a Standard brings CI, so only a Container with one has checks to
// require.
func Protected(hasStandard bool) bool { return hasStandard }

// ProjectNotesPath is the project's own word to its agents about its workspace
// and ways, read after AGENTS.md when the repository has it. No Standard
// protects it, so any issue may change it; the factory writes no such file.
const ProjectNotesPath = "docs/project-notes.md"

// ReleaseTags is the tag pattern that releases a Container; only the operator's
// Git user may push such tags, since release jobs hold the signing keys.
const ReleaseTags = "v*"

// AgentProtectedPaths returns the paths agents may not change in a Container:
// the platform's for its CI system and its Standard's, sorted and without
// duplicates.
func AgentProtectedPaths(ci CI, standard []string) []string {
	return sortedPaths(PlatformProtectedPaths(ci), standard)
}

// TouchedPaths returns the changed paths that fall under a protected path: equal
// to a file, or inside a directory ending in "/".
func TouchedPaths(changed, protected []string) []string {
	var out []string
	for _, p := range changed {
		for _, prot := range protected {
			if covers(prot, p) {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// covers reports whether path is the protected file or inside the protected
// directory (a path ending in "/").
func covers(prot, path string) bool {
	return path == prot || (strings.HasSuffix(prot, "/") && strings.HasPrefix(path, prot))
}

// ClusterNamespace returns the namespace of an in-cluster service address, such
// as "gitea" for http://gitea.gitea.svc.cluster.local:3000.
func ClusterNamespace(host string) (string, bool) {
	labels := strings.Split(host, ".")
	if len(labels) >= 3 && labels[2] == "svc" {
		return labels[1], true
	}
	return "", false
}
