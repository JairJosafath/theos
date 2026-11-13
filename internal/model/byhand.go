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
	"unicode/utf8"
)

// A by-hand issue holds the acceptance criteria an approved review accepted as
// open (OpenCriterion): they need what no agent's sandbox has, so a person does
// them. The operator files one on merge, status/needs-human in the Later
// milestone, so no Crew picks it and no release waits on it
// (plans/done/honest-evidence.md, decisions 4-5).

// LabelByHand marks an issue only a person can do.
const LabelByHand = "kind/by-hand"

// ByHandLabel is the label the operator keeps for by-hand issues.
func ByHandLabel() IssueLabel {
	return IssueLabel{LabelByHand, "#e99695", "Needs a person: agents can't do it; filed by the operator", false}
}

// IsByHand reports whether an issue's labels mark it for a person.
func IsByHand(labels []string) bool { return slices.Contains(labels, LabelByHand) }

// ByHandMarker is the hidden line that ties a by-hand issue to the Task that
// filed it, so a rerun of the merge finds it again.
func ByHandMarker(task string) string {
	return "<!-- theos:by-hand " + task + " -->"
}

// HasByHandMarker reports whether body carries task's marker on a line of its
// own, as ByHandIssue writes it.
func HasByHandMarker(body, task string) bool {
	marker := ByHandMarker(task)
	for line := range strings.SplitSeq(body, "\n") {
		if strings.TrimSpace(line) == marker {
			return true
		}
	}
	return false
}

// byHandWhy starts the line under each criterion that says why a person does it.
const byHandWhy = "Needs a person: "

// ByHandIssue renders the issue for a person filed when Task task merged pull
// request pr for issue number issue, titled issueTitle, with the criteria the
// review, by reviewer by, accepted as open: each with its why, as an
// "Acceptance criteria" checklist. envs are the test environments the
// Task's runs leased: the issue names them, and a criterion left open with
// no why gets one that says so, so a person sees what they had
// (docs/plans/done/test-environments.md, T13).
func ByHandIssue(issueTitle string, issue int, pr, task, by string, open []OpenCriterion,
	envs []string) (title, body string) {
	if issueTitle = strings.Join(strings.Fields(issueTitle), " "); issueTitle == "" {
		issueTitle = fmt.Sprintf("#%d", issue)
	}
	title = clipTitle("By hand: " + issueTitle)
	var b strings.Builder
	has := "no agent's sandbox has"
	if len(envs) > 0 {
		has = "neither an agent's sandbox nor the test environments its runs had (" + envLabels(envs) + ") have"
	}
	fmt.Fprintf(&b, "Task `%s` merged %s for #%d. %s approved it with the criteria below left open: they need "+
		"what %s (a person's desktop or device, an account, a purchase), so a person does them.\n",
		task, pr, issue, by, has)
	b.WriteString("\n## Acceptance criteria\n")
	for _, o := range open {
		fmt.Fprintf(&b, "\n- [ ] %s", strings.Join(strings.Fields(o.Criterion), " "))
		why := strings.Join(strings.Fields(o.Why), " ")
		if why == "" && len(envs) > 0 {
			why = fmt.Sprintf("the review gave no reason, though the runs had %s: see whether it can be met there "+
				"before doing it by hand.", joinAnd(envs))
		}
		if why != "" {
			fmt.Fprintf(&b, "\n  %s%s", byHandWhy, why)
		}
	}
	fmt.Fprintf(&b, "\n\n---\nAgents don't pick this up (%s). Check each criterion once it is done, and close "+
		"the issue; close it too if it is dropped.\n\n", LabelNeedsHuman)
	// The criteria are the agents' words: no comment of theirs may pass for a
	// marker, so the operator never takes this issue for another Task's.
	return title, strings.ReplaceAll(b.String(), "<!--", "&lt;!--") + ByHandMarker(task) + "\n"
}

// ByHandReason is why a Brief lists a by-hand issue: "Needs a person: " and
// the whys its body gives, else a pointer to the issue.
func ByHandReason(body string) string {
	var whys []string
	for line := range strings.SplitSeq(body, "\n") {
		if why, ok := strings.CutPrefix(strings.TrimSpace(line), byHandWhy); ok && strings.TrimSpace(why) != "" {
			whys = append(whys, strings.TrimRight(strings.TrimSpace(why), "."))
		}
	}
	if len(whys) == 0 {
		return "Needs a person; see the issue."
	}
	return byHandWhy + strings.Join(whys, "; ") + "."
}

// clipTitle cuts an issue title to maxIssueTitle bytes at a rune, ending it
// with "…".
func clipTitle(title string) string {
	if len(title) <= maxIssueTitle {
		return title
	}
	const ellipsis = "…"
	cut := maxIssueTitle - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(title[cut]) {
		cut--
	}
	return title[:cut] + ellipsis
}
