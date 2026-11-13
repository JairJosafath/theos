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
	"strings"
)

// A Crew's first issue on a repository it adopted with no Standard is its
// own (docs/plans/done/adopt-existing.md, step 5; #98): the factory scaffolded
// nothing there, so nothing tells an agent how the repository is built. The
// Crew files one issue, once, to write AGENTS.md and say what it found, and
// works it as any other.

// LabelOnboarding marks the issue a Crew files once on a repository it
// adopted, to write the guide its agents read.
const LabelOnboarding = "kind/onboarding"

// OnboardingLabel is the label the operator keeps for that issue.
func OnboardingLabel() IssueLabel {
	return IssueLabel{LabelOnboarding, "#006b75",
		"The Crew's first issue on a repository it adopted: write AGENTS.md and say what it found", false}
}

// AgentsGuidePath is the guide every agent reads first in a repository.
const AgentsGuidePath = "AGENTS.md"

// onboardingMarker ends the onboarding issue's body, so the Crew finds the
// one it filed and files no second.
const onboardingMarker = "<!-- theos-onboarding -->"

// HasOnboardingMarker reports whether an issue's body is the onboarding
// issue's.
func HasOnboardingMarker(body string) bool {
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) == onboardingMarker {
			return true
		}
	}
	return false
}

// OnboardingFacts are what a Crew's onboarding issue depends on.
type OnboardingFacts struct {
	// Adopted means the Crew's Container adopts its repository with no
	// Standard, whose agent guides a scaffolded repository has.
	Adopted bool
	// Unprotected says why its main isn't protected yet; empty once it is.
	Unprotected string
	// Paused is the Crew's spec.paused.
	Paused bool
	// Filed means the Crew's status records the issue it filed.
	Filed bool
}

// FilesOnboarding reports whether the Crew files its onboarding issue now:
// once, on a repository it adopted with no Standard, when agents may work
// there and the Crew picks up issues. The caller still looks for an open
// issue with the marker, which a lost status write leaves.
func FilesOnboarding(f OnboardingFacts) bool {
	return f.Adopted && f.Unprotected == "" && !f.Paused && !f.Filed
}

// OnboardingIssue renders the issue: what to write, in the one file, and
// criteria a reviewer can check. hasGuide means the repository has an
// AGENTS.md already, which the issue then asks to check and complete.
func OnboardingIssue(hasGuide bool) (title, body string) {
	title = "Write " + AgentsGuidePath + " for this repository, and say what you found"
	what := "This repository has no `" + AgentsGuidePath + "`. Write it."
	if hasGuide {
		title = "Check and complete " + AgentsGuidePath + ", and say what you found"
		what = "This repository has an `" + AgentsGuidePath + "`. Check each thing it says against the " +
			"repository as it is, correct what is wrong and add what is missing."
	}
	var b strings.Builder
	b.WriteString("The factory adopted this repository as it is: it scaffolded nothing here, so nothing tells an " +
		"agent how the repository is built. Every agent reads `" + AgentsGuidePath + "` before it works. " + what +
		"\n\n## Goal\n\n")
	b.WriteString("`" + AgentsGuidePath + "` at the repository's root says, from what the repository holds and " +
		"what you ran, never from a guess:\n\n" +
		"- how to build it, how to run its tests and how to run it, each with the command;\n" +
		"- where things are: the code, the tests, the CI workflows, the configuration;\n" +
		"- what its conventions are, where the code shows them (naming, layout, how errors and logs are written);\n" +
		"- what not to touch, and why, where the repository says so.\n\n" +
		"Change no other file. In the pull request's description, list what you found that a person should " +
		"know: a test that fails on main, a check that needs a secret or a service, a command the README names " +
		"that doesn't work, a dependency without a lockfile. Say \"nothing found\" when that is so.\n\n")
	b.WriteString("## Acceptance criteria\n\n" +
		"- [ ] `" + AgentsGuidePath + "` gives the commands to build, to test and to run the repository, and each " +
		"was run\n" +
		"- [ ] It says where the code, the tests and the CI workflows are\n" +
		"- [ ] The pull request changes no file but `" + AgentsGuidePath + "`\n" +
		"- [ ] The pull request's description lists what was found, or says nothing was\n\n")
	return title, b.String() + onboardingMarker + "\n"
}
