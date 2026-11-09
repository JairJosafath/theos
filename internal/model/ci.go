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
}

// CIGiteaActions names Gitea Actions, the CI system of a Gitea host.
const CIGiteaActions = "giteaActions"

// ciSystems are the CI systems the factory knows, by name.
var ciSystems = map[string]CI{
	// Gitea Actions reports a context per workflow, job and event, such as
	// "check / check (pull_request)", and reads .gitea/.
	CIGiteaActions: {Name: CIGiteaActions, CheckContexts: []string{"check*"}, ProtectedPaths: []string{".gitea/"}},
}

// hostCI is each Git host's own CI system, which a Container uses unless its
// GitProvider names another.
var hostCI = map[string]string{"gitea": CIGiteaActions}

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
