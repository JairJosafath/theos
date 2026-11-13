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
	"cmp"
	"fmt"
	"slices"
)

// A failed Task's pull request stays open while a later try at its issue may
// still continue it (#607), and is closed once none will: its issue is
// closed, a newer try opened its own pull request, the issue's tries are used
// up, or the Task had no issue for a try to come back to. The branch stays, so
// a person can reopen it (plans/done/abandoned-pull-requests.md).

// IssueTry is one Task at an issue of a Container, failed or not.
type IssueTry struct {
	Name string
	// Created orders the tries; Name breaks a tie.
	Created int64
	// Final is a try that ended; Failed one that failed; NoCI one that failed
	// through its CI runner's fault (RunnerFault), which costs no try; Unseen
	// one whose agent couldn't see the issue (ReasonIssueUnseen), the first
	// of which costs no try (TryCounter).
	Final, Failed, NoCI, Unseen bool
	// PullRequest is the number of the try's pull request, 0 before it has one.
	PullRequest    int
	PullRequestURL string
}

// AbandonedPullFacts are what decides a failed Task's pull request.
type AbandonedPullFacts struct {
	Task string
	// Issue is the Task's issue, 0 for a Task with a goal instead.
	Issue       int
	IssueClosed bool
	// PullOpen and PullMerged are the pull request's state on the Git host.
	PullOpen, PullMerged bool
	PullRequest          int
	// Tries are every Task at Issue in the Container, the failed one included.
	Tries []IssueTry
}

// PullEnd is what to do with a failed Task's pull request.
type PullEnd struct {
	// Close closes the pull request with Comment.
	Close   bool
	Comment string
	// Ended says how the pull request ended, for the Task's status; empty while
	// it stays open and the Task looks again later.
	Ended string
}

// AbandonedPull decides whether a failed Task's pull request is closed now,
// kept for a later try, or no longer the Task's to look after.
func AbandonedPull(f AbandonedPullFacts) PullEnd {
	switch {
	case f.PullMerged:
		return PullEnd{Ended: "merged on the Git host"}
	case !f.PullOpen:
		return PullEnd{Ended: "closed on the Git host"}
	}
	for _, t := range newerTries(f.Task, f.Tries) {
		if t.PullRequest == f.PullRequest {
			return PullEnd{Ended: fmt.Sprintf("continued by Task %s", t.Name)}
		}
	}
	for _, t := range newerTries(f.Task, f.Tries) {
		if t.PullRequest != 0 {
			return closePull(f.Task, fmt.Sprintf("Task `%s`, a newer try at #%d, works on %s",
				t.Name, f.Issue, t.PullRequestURL), fmt.Sprintf("superseded by Task %s", t.Name))
		}
	}
	for _, t := range newerTries(f.Task, f.Tries) {
		if !t.Final {
			// It may still take this pull request up (#607); a closed issue
			// ends it at its next run, and this Task looks again then.
			return PullEnd{}
		}
	}
	failed := 0
	var counter TryCounter
	for _, t := range f.Tries {
		if t.Failed && counter.Costs(t.NoCI, t.Unseen) {
			failed++
		}
	}
	switch {
	case f.Issue == 0:
		return closePull(f.Task, "the Task had no issue, so no later try continues it", "closed: no issue")
	case f.IssueClosed:
		return closePull(f.Task, fmt.Sprintf("issue #%d is closed", f.Issue),
			fmt.Sprintf("closed: issue #%d closed", f.Issue))
	case failed >= MaxIssueAttempts:
		return closePull(f.Task, fmt.Sprintf("its issue's %d tries are used up, so a Crew starts no more", failed),
			"closed: tries used up")
	}
	return PullEnd{}
}

// newerTries are the tries at the issue started after task, oldest first.
func newerTries(task string, tries []IssueTry) []IssueTry {
	sorted := slices.Clone(tries)
	slices.SortFunc(sorted, func(a, b IssueTry) int {
		return cmp.Or(cmp.Compare(a.Created, b.Created), cmp.Compare(a.Name, b.Name))
	})
	i := slices.IndexFunc(sorted, func(t IssueTry) bool { return t.Name == task })
	if i < 0 {
		return nil
	}
	return sorted[i+1:]
}

func closePull(task, why, ended string) PullEnd {
	return PullEnd{Close: true, Ended: ended, Comment: fmt.Sprintf(
		"Closing this pull request: Task `%s` failed, and %s. Its branch stays, so it can be reopened.", task, why)}
}
