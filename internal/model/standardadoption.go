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

// A Standard is something an adopted repository grows into
// (docs/plans/adopt-existing.md, decision 7; #97). A Container that adopts
// its repository and names a Standard main doesn't hold is Ready on its
// identity, and the Standard's files arrive as a pull request a person
// merges or closes. Until main holds the files the Standard protects, the
// Standard isn't recorded, main isn't protected and no agent works there.

// StandardAdoptionFacts is everything the next step of bringing a Standard
// into an adopted repository depends on: the adoption's state from status, and
// what the operator read from the Git host so far in this pass. The reads
// start out unknown; a step that needs one asks for it.
type StandardAdoptionFacts struct {
	// Name is the Standard, and Target its template ref with the Container's
	// effective checks.
	Name   string
	Target StandardVersion
	// Adoption is the adoption's pull request, open or stopped, or nil.
	Adoption *StandardAdoptionInFlight
	// PR is the adoption's pull request, nil until read.
	PR *UpdatePullRequest
	// Main is main's head, "" until read.
	Main string
	// Diff is what main holds differently from the Standard's render, nil
	// until compared.
	Diff *AdoptionDiff
}

// StandardAdoptionInFlight is an adoption's state in status.
type StandardAdoptionInFlight struct {
	// Version is what the pull request renders the Standard's files at.
	Version        StandardVersion
	PullRequestURL string
	// Stopped says the adoption stopped: its pull request merged with a file
	// the Standard protects left differing (Merged), or a person closed it.
	Stopped, Merged bool
	// Base is main's head the files were last compared at.
	Base string
}

// AdoptionDiff is what main holds differently from a Standard's render.
type AdoptionDiff struct {
	// Paths are the Standard's files main is missing or holds with other
	// content, and Protected the ones of them the Standard protects.
	Paths, Protected []string
}

// AdoptionAction is what the operator does next to bring a Standard into an
// adopted repository.
type AdoptionAction string

// Standard adoption actions.
const (
	// AdoptionNone leaves a stopped adoption as it is: main is as it was last
	// compared, and the condition keeps saying why it stopped.
	AdoptionNone AdoptionAction = "None"
	// AdoptionReadPR reads the adoption's pull request into PR.
	AdoptionReadPR AdoptionAction = "ReadPR"
	// AdoptionReadMain reads main's head into Main.
	AdoptionReadMain AdoptionAction = "ReadMain"
	// AdoptionCompare renders the Standard at the step's Version and compares
	// it with main's files, into Diff and Main.
	AdoptionCompare AdoptionAction = "Compare"
	// AdoptionOpen opens the pull request that writes Diff.Paths as the
	// Standard renders them at the step's Version.
	AdoptionOpen AdoptionAction = "Open"
	// AdoptionWait reports the pull request open, waiting on a person.
	AdoptionWait AdoptionAction = "Wait"
	// AdoptionRecord records the Standard at the step's Version and protects
	// main: main holds the files the Standard protects as it renders them.
	AdoptionRecord AdoptionAction = "Record"
	// AdoptionStopClosed stops an adoption whose pull request a person closed
	// while main is without files the Standard protects.
	AdoptionStopClosed AdoptionAction = "StopClosed"
	// AdoptionStopDiffer stops an adoption whose pull request merged while main
	// holds files the Standard protects with other content.
	AdoptionStopDiffer AdoptionAction = "StopDiffer"
	// AdoptionForget forgets a stopped adoption the Standard's ref or the
	// Container's checks moved past, so it is planned again.
	AdoptionForget AdoptionAction = "Forget"
)

// AdoptionStep is the next step of a Standard's adoption.
type AdoptionStep struct {
	Action AdoptionAction
	// Version is what the Standard renders at for Compare, Open and Record.
	Version StandardVersion
	// Message is the condition's message for a step that reports or stops the
	// adoption.
	Message string
}

// NextAdoptionStep decides the next step of bringing a Standard into an
// adopted repository. The operator takes it, adds what it read to the facts,
// and asks again until a step ends the pass: every step but the reads,
// Compare and Forget does.
//
// An open pull request stays at the version it was planned at, whatever the
// Standard's ref does meanwhile: once it merges the Standard is recorded at
// that version, and an ordinary Standard update brings it on. A stopped
// adoption is compared again when main moved, so files a person brings by
// hand are found, and planned again when the Standard's ref or the
// Container's checks change.
func NextAdoptionStep(f StandardAdoptionFacts) AdoptionStep {
	a := f.Adoption
	target := f.Target.Normal()
	if a == nil {
		switch {
		case f.Diff == nil:
			return AdoptionStep{Action: AdoptionCompare, Version: target}
		case len(f.Diff.Paths) == 0:
			return AdoptionStep{Action: AdoptionRecord, Version: target}
		}
		return AdoptionStep{Action: AdoptionOpen, Version: target}
	}
	version := a.Version.Normal()
	switch {
	case a.Stopped && version != target:
		return AdoptionStep{Action: AdoptionForget}
	case a.Stopped && f.Main == "":
		return AdoptionStep{Action: AdoptionReadMain}
	case a.Stopped && f.Main == a.Base:
		return AdoptionStep{Action: AdoptionNone}
	case a.Stopped:
	case f.PR == nil:
		return AdoptionStep{Action: AdoptionReadPR}
	case !f.PR.Merged && !f.PR.Closed:
		return AdoptionStep{Action: AdoptionWait, Message: fmt.Sprintf(
			"Pull request %s brings the files of %s at %s into the repository; a person merges it, or closes it to "+
				"leave the repository without them. No agent works on the repository until it merges",
			a.PullRequestURL, f.Name, version)}
	}
	// A person who merged the pull request may have changed, in it, the files
	// the Standard doesn't protect: those are the repository's own from then
	// on. One who closed it took none of them, so only a main that holds
	// every file, brought by hand, has the Standard.
	merged := a.Merged || f.PR != nil && f.PR.Merged
	switch {
	case f.Diff == nil:
		return AdoptionStep{Action: AdoptionCompare, Version: version}
	case len(f.Diff.Paths) == 0, merged && len(f.Diff.Protected) == 0:
		return AdoptionStep{Action: AdoptionRecord, Version: version}
	case merged:
		return AdoptionStep{Action: AdoptionStopDiffer, Message: fmt.Sprintf(
			"Pull request %s merged, but main holds %s with other content than %s renders at %s, and %s protects "+
				"them. No agent works on the repository until main holds them as rendered; a change of the "+
				"Standard's ref or of the Container's checks opens a new pull request",
			a.PullRequestURL, strings.Join(f.Diff.Protected, ", "), f.Name, version, f.Name)}
	}
	return AdoptionStep{Action: AdoptionStopClosed, Message: fmt.Sprintf(
		"Pull request %s was closed without merging, so the repository is without %s as %s renders them at %s. "+
			"No agent works on the repository until main holds them; a change of the Standard's ref or of the "+
			"Container's checks opens a new pull request",
		a.PullRequestURL, strings.Join(f.Diff.Paths, ", "), f.Name, version)}
}

// ClosestRender picks, among the renders an adoption tried, the one main is
// closest to, given how many paths differ from each: the renders are in
// AdoptionDomains' order, the last one with no Domains. A render with Domains
// wins only by being strictly closer than the one with none, the earlier one
// on a tie between two: a repository that shows no trace of a Domain gets the
// Standard's files without one, and DomainsApplied says so.
func ClosestRender(differ []int) int {
	none := len(differ) - 1
	best := none
	for i := none - 1; i >= 0; i-- {
		if differ[i] < differ[none] && (best == none || differ[i] <= differ[best]) {
			best = i
		}
	}
	return best
}

// StandardAdoptionBranch is the branch that brings the Standard name at
// version v into an adopted repository: "standard-adopt/<name>-<ref>", ref and
// checks as in StandardUpdateBranch.
func StandardAdoptionBranch(name string, v StandardVersion) string {
	return "standard-adopt/" + strings.TrimPrefix(StandardUpdateBranch(name, v), "standard-update/")
}

// StandardAdoptionTitle is the title of that pull request and its commit.
func StandardAdoptionTitle(name string, v StandardVersion) string {
	return "Bring in the Standard " + name + " at " + v.Normal().String()
}

// StandardAdoptionBody is that pull request's description: what it writes,
// which of the files the Standard protects, and what merging or closing it
// means.
func StandardAdoptionBody(name string, v StandardVersion, diff AdoptionDiff) string {
	var b strings.Builder
	fmt.Fprintf(&b, "This repository was adopted with the Standard `%s`, and `main` doesn't hold these files as "+
		"the Standard renders them at `%s`. This pull request writes them:\n\n", name, v.Normal().Ref)
	for _, p := range diff.Paths {
		fmt.Fprintf(&b, "- `%s`", p)
		if slices.Contains(diff.Protected, p) {
			b.WriteString(" (protected)")
		}
		b.WriteString("\n")
	}
	b.WriteString("\nA file that was there is replaced: read the difference as any change, and push to this " +
		"branch what the repository must keep. The operator never merges this pull request.\n\n" +
		"- **Merge it** once the protected files are as the Standard has them: the operator then records the " +
		"Standard, protects `main` with its checks, and agents may work on the repository.\n" +
		"- **Close it** to leave the repository without the Standard's files: no agent works on it until `main` " +
		"holds the protected ones.\n")
	return b.String()
}
