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
	"maps"
	"slices"
	"strings"
)

// CI is what a CI system means to a Container: where its files go, the check
// contexts a protected default branch requires and the paths the platform
// protects for it. Each CI system is one row in ciSystems.
type CI struct {
	// Name names the CI system as GitProvider.spec.ci's member does, such as
	// giteaActions. A template keeps the system's files under
	// .theos/ci/<Name>/.
	Name string
	// CheckContexts are the status check contexts a protected default branch
	// requires, as glob patterns. A Standard's CI must report under a context
	// that matches one of them.
	CheckContexts []string
	// ProtectedPaths are the paths the CI system reads, which no agent may
	// change. A path ending in "/" is a directory.
	ProtectedPaths []string
	// FallbackPaths are the paths the CI system reads workflows from when a
	// repository has none under ProtectedPaths. A repository the factory
	// scaffolded has its workflows where the system looks first; one it
	// adopted may have them here (ForAdopted).
	FallbackPaths []string
}

// ForAdopted is the CI system as a Container that adopts its repository
// with no Standard uses it (docs/plans/adopt-existing.md, step 2): the check
// contexts the Container names, the system's own when it names none, and the
// system's fallback paths protected beside its own, since the repository's
// workflows may be there.
func (ci CI) ForAdopted(contexts []string) CI {
	if len(contexts) > 0 {
		ci.CheckContexts = slices.Clone(contexts)
	}
	ci.ProtectedPaths = sortedPaths(ci.ProtectedPaths, ci.FallbackPaths)
	return ci
}

// Reasons of a Container's Protected condition, which one that adopts its
// repository with no Standard has.
const (
	// ReasonProtected: main's head reports a check its contexts match, and
	// main requires them.
	ReasonProtected = "Protected"
	// ReasonNoMatchingCheck: main's head reports no such check, so main
	// isn't protected and no agent works on the repository.
	ReasonNoMatchingCheck = "NoMatchingCheck"
)

// AdoptedChecks says whether an adopted repository's default branch can be
// protected by required, the check contexts its Container asks for: its
// head, whose checks by context are contexts, reports at least one check
// that matches each pattern, skipped ones aside. A pattern nothing reports
// would block every merge, the people's too, so the operator requires none
// until each has a check. message says which checks main then requires, or
// what the head reports instead, for the Container's Protected condition.
func AdoptedChecks(required []string, contexts map[string]string) (ok bool, message string) {
	var missing []string
	for _, pattern := range required {
		if len(RequiredChecks([]string{pattern}, contexts)) == 0 {
			missing = append(missing, pattern)
		}
	}
	if len(missing) == 0 {
		return true, fmt.Sprintf("main requires the checks matching %s, which its head reports (%s)",
			joinAnd(required), joinAnd(RequiredChecks(required, contexts)))
	}
	reported := make([]string, 0, len(contexts))
	for context, state := range contexts {
		if state != checkSkipped {
			reported = append(reported, context)
		}
	}
	slices.Sort(reported)
	reports := "it reports no check at all: add a CI workflow, and the factory looks again once main has a result"
	if len(reported) > 0 {
		const shown = 8
		more := ""
		if len(reported) > shown {
			more = fmt.Sprintf(" and %d more", len(reported)-shown)
			reported = reported[:shown]
		}
		reports = fmt.Sprintf("it reports %s%s: name the repository's own in spec.checkContexts, a pattern such "+
			"as \"ci / test*\" for a workflow's job on every event", joinAnd(reported), more)
	}
	return false, fmt.Sprintf("main's head reports no check matching %s, so main isn't protected and no agent "+
		"works on the repository; %s", joinAnd(missing), reports)
}

// CIGiteaActions names Gitea Actions, the CI system of a Gitea host.
const CIGiteaActions = "giteaActions"

// CIGitHubActions names GitHub Actions, the CI system of a GitHub host
// (docs/plans/github-host.md, step 5).
const CIGitHubActions = "githubActions"

// ciSystems are the CI systems the factory knows, by name.
var ciSystems = map[string]CI{
	// Gitea Actions reports a context per workflow, job and event, such as
	// "check / check (pull_request)", and reads .gitea/.
	// It reads .github/workflows/ when a repository has no workflows where it
	// looks first.
	CIGiteaActions: {Name: CIGiteaActions, CheckContexts: []string{"check*"}, ProtectedPaths: []string{".gitea/"},
		FallbackPaths: []string{".github/workflows/"}},
	// GitHub Actions reports each job as a check run under the job's name,
	// such as "check", whatever the event, and reads .github/workflows/ and
	// nowhere else. All of .github/ is the platform's there: the issue and
	// pull request templates and the code owners beside the workflows.
	CIGitHubActions: {Name: CIGitHubActions, CheckContexts: []string{"check*"}, ProtectedPaths: []string{".github/"}},
}

// hostCI is each Git host's own CI system, which a Container uses unless its
// GitProvider names another.
var hostCI = map[string]string{"gitea": CIGiteaActions, "github": CIGitHubActions}

// CINames names the CI systems the factory knows, sorted.
func CINames() []string {
	return slices.Sorted(maps.Keys(ciSystems))
}

// CIByName returns the CI system name names.
func CIByName(name string) (CI, error) {
	ci, ok := ciSystems[name]
	if !ok {
		return CI{}, fmt.Errorf("unknown CI system %q", name)
	}
	return ci, nil
}

// HostCI returns a Git host's own CI system, such as Gitea Actions for gitea.
func HostCI(host string) (CI, error) {
	name, ok := hostCI[host]
	if !ok {
		return CI{}, fmt.Errorf("git host %q has no CI system the factory knows", host)
	}
	return CIByName(name)
}

// WorkflowDirs are where the CI systems the factory knows read a repository's
// workflows, in the order a host looks: workflows/ under each system's own
// paths, then its fallbacks. The adoption detector reads them (adopt.go).
func WorkflowDirs() []string {
	var own, fallback []string
	for _, name := range slices.Sorted(maps.Keys(ciSystems)) {
		ci := ciSystems[name]
		for _, dir := range ci.ProtectedPaths {
			if strings.HasSuffix(dir, "/") {
				own = append(own, dir+"workflows/")
			}
		}
		fallback = append(fallback, ci.FallbackPaths...)
	}
	out := slices.Concat(own, fallback)
	return slices.Compact(out)
}

// PlatformProtectedPaths are the paths that belong to the operator and the CI
// system: no agent may change them, whatever the Standard. Sorted.
func PlatformProtectedPaths(ci CI) []string {
	return sortedPaths(OperatorPaths, ci.ProtectedPaths)
}

func sortedPaths(lists ...[]string) []string {
	out := slices.Concat(lists...)
	slices.Sort(out)
	return slices.Compact(out)
}
