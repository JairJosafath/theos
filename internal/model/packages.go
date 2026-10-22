/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package model

import (
	"fmt"
	"strings"
)

// States of a release's npm and PyPI packages, status.lastRelease.packages.state.
const (
	PackagesStateWaiting   = "Waiting"
	PackagesStatePublished = "Published"
	PackagesStateNoPackage = "NoPackage"
	PackagesStateInvalid   = "Invalid"
	PackagesStateFailed    = "Failed"
)

// Reasons of the PackagesPublished condition.
const (
	PackagesReasonPublished       = "PackagesPublished"
	PackagesReasonNoPackage       = "NoPackage"
	PackagesReasonInvalid         = "InvalidPackage"
	PackagesReasonGitFailure      = "GitHostUnavailable"
	PackagesReasonWaitsForRelease = "WaitsForReleaseRun"
)

// Outcomes of one package file of a release, as the Git host's adapter reports
// them: uploaded now, in the registry already, an npm name outside the scope,
// left alone, or a file that can't be published at the tag's version.
const (
	PackagePublished = "Published"
	PackageExists    = "Exists"
	PackageUnscoped  = "Unscoped"
	PackageInvalid   = "Invalid"
)

// ReleasePackage is what became of one package file of a release.
type ReleasePackage struct {
	// Outcome is PackagePublished, PackageExists, PackageUnscoped or PackageInvalid.
	Outcome string
	// Ecosystem is npm or PyPI.
	Ecosystem string
	// Name and Version are the package's, as its metadata declares them.
	Name    string
	Version string
	// File is the release asset's name.
	File string
	// Reason says why a package is Unscoped or Invalid.
	Reason string
}

// PublishedPackage is one package a release has in its registry.
type PublishedPackage struct {
	Ecosystem string
	Name      string
	Version   string
}

// PackagesFacts is what the Crew knows of its last release's packages: the
// tag, the release runs' result (RunSucceeded once they all ended well), the
// error of the publish when the host couldn't be reached, else what became of
// each package file.
type PackagesFacts struct {
	Tag      string
	Run      string
	Failure  string
	Packages []ReleasePackage
}

// PackagesDecision is what to say about a release's packages: its state, the
// PackagesPublished condition's value (Done), reason and message, and the
// packages the release has in its registries.
type PackagesDecision struct {
	State     string
	Done      bool
	Reason    string
	Message   string
	Published []PublishedPackage
}

// DecidePackages says how publishing a release's packages went. The release
// runs build the package files, so nothing is read before they succeed; a host
// that can't be reached is tried again; a file that can't be published at the
// tag's version fails the release's packages, with the others still listed; a
// release with no package file is done, as is one whose files are all in their
// registries. An npm tarball outside the scope is left alone and named in the
// message, with the scope it lacks, and never fails the release.
func DecidePackages(f PackagesFacts) PackagesDecision {
	if f.Run != RunSucceeded {
		return PackagesDecision{State: PackagesStateWaiting, Reason: PackagesReasonWaitsForRelease,
			Message: fmt.Sprintf("Release runs of %s are %s; its packages are read from the release once they succeed",
				f.Tag, f.Run)}
	}
	if f.Failure != "" {
		return PackagesDecision{State: PackagesStateFailed, Reason: PackagesReasonGitFailure,
			Message: fmt.Sprintf("Could not publish the packages of %s: %s", f.Tag, f.Failure)}
	}
	var published []PublishedPackage
	var names, invalid, unscoped []string
	for _, p := range f.Packages {
		switch p.Outcome {
		case PackagePublished, PackageExists:
			published = append(published, PublishedPackage{Ecosystem: p.Ecosystem, Name: p.Name, Version: p.Version})
			names = append(names, fmt.Sprintf("%s %s %s", p.Ecosystem, p.Name, p.Version))
		case PackageInvalid:
			invalid = append(invalid, fmt.Sprintf("Did not publish %s: %s", p.File, p.Reason))
		case PackageUnscoped:
			unscoped = append(unscoped, fmt.Sprintf("left %s alone: %s", p.File, p.Reason))
		}
	}
	if len(invalid) > 0 {
		return PackagesDecision{State: PackagesStateInvalid, Reason: PackagesReasonInvalid,
			Message: strings.Join(invalid, "; "), Published: published}
	}
	note := ""
	if len(unscoped) > 0 {
		note = "; " + strings.Join(unscoped, "; ")
	}
	if len(published) == 0 {
		return PackagesDecision{State: PackagesStateNoPackage, Done: true, Reason: PackagesReasonNoPackage,
			Message: fmt.Sprintf("%s attached no npm tarball, wheel or sdist; no package to publish%s", f.Tag, note)}
	}
	return PackagesDecision{State: PackagesStatePublished, Done: true, Reason: PackagesReasonPublished,
		Message: "Published " + strings.Join(names, " and ") + note, Published: published}
}
