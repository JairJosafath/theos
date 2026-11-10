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
	"strings"
)

// A Standard update whose CI fails is the Crew's to fix: the operator files an
// issue in the Container's repository, once per target ref
// (plans/standard-update-fails.md, decisions 1 and 2).

// StandardFailure is a Standard update whose CI failed.
type StandardFailure struct {
	// Standard is the Standard's name; Ref the template ref the update targets.
	Standard, Ref string
	// Checks is CI's state on the update's commit; ChecksURL the failed run, or
	// empty when the host gave none.
	Checks, ChecksURL string
	// PullRequestURL and Branch are the update's pull request and its branch;
	// Commit its head.
	PullRequestURL, Branch, Commit string
}

// StandardFailureMarker is the line that ties a failure issue to its Standard
// and ref.
func StandardFailureMarker(standard, ref string) string {
	return "<!-- theos:standard-fails " + standard + " " + ref + " -->"
}

// HasStandardFailureMarker reports whether body carries the marker of the
// Standard's ref on a line of its own, as StandardFailureIssue writes it.
func HasStandardFailureMarker(body, standard, ref string) bool {
	marker := StandardFailureMarker(standard, ref)
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

// StandardFailureDue says whether a failure of the update to ref still needs
// its issue, filed being the ref the last one was filed for: one per target
// ref, so a re-plan or a restart files nothing twice.
func StandardFailureDue(filed, ref string) bool {
	return filed != ref
}

// StandardFailureIssue renders the issue that asks the Crew to make main pass
// the check of the Standard at the update's ref.
func StandardFailureIssue(f StandardFailure) (title, body string) {
	title = fmt.Sprintf("Standard %s %s fails this repository's check", f.Standard, f.Ref)
	var s strings.Builder
	fmt.Fprintf(&s, "The operator's update of the Standard %s to `%s` (%s) fails this repository's CI", f.Standard, f.Ref,
		f.PullRequestURL)
	fmt.Fprintf(&s, ": CI is %s on `%s`", f.Checks, f.Commit)
	if f.ChecksURL != "" {
		fmt.Fprintf(&s, ", %s", f.ChecksURL)
	}
	s.WriteString(".\n\n")
	s.WriteString("The update changes only the Standard's files, so what fails is this repository's own code " +
		"meeting the Standard's new check. The failed run's message says what to write.\n\n")
	fmt.Fprintf(&s, "## Goal\n\nThis repository's main passes the check of the Standard %s at `%s`.\n\n", f.Standard, f.Ref)
	s.WriteString("## Acceptance criteria\n\n")
	s.WriteString("- [ ] What the failed run reports is fixed on main, in a pull request of its own\n")
	fmt.Fprintf(&s, "- [ ] Nothing is pushed to the update's branch `%s`: the operator stops an update someone else "+
		"pushed to\n", f.Branch)
	s.WriteString("- [ ] `make check` passes\n\n")
	fmt.Fprintf(&s, "Once main moves, the operator closes %s and opens the update again from the new main, by "+
		"itself; it merges once CI passes.\n\n", f.PullRequestURL)
	body = strings.ReplaceAll(s.String(), "<!--", "&lt;!--") + StandardFailureMarker(f.Standard, f.Ref) + "\n"
	return title, body
}
