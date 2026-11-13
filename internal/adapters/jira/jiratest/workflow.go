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

package jiratest

import "slices"

// The keys of a status category, as Jira's statuses carry them.
const (
	CategoryNew           = "new"
	CategoryIndeterminate = "indeterminate"
	CategoryDone          = "done"
)

// The names of the statuses and the resolutions of a standard Jira Cloud
// site, which the workflows below use.
const (
	StatusToDo       = "To Do"
	StatusInProgress = "In Progress"
	StatusDone       = "Done"
	ResolutionDone   = "Done"
	ResolutionWontDo = "Won't Do"
)

// Status is a status of a workflow, in the category whose key is Category.
type Status struct {
	Name, Category string
}

// Screen is what a transition's screen holds of the resolution.
type Screen int

const (
	// NoScreen: the transition has no resolution on its screen, as a
	// team-managed project's has none.
	NoScreen Screen = iota
	// TakesResolution: the screen has the field, which may stay empty.
	TakesResolution
	// RequiresResolution: the screen has the field, and it is required.
	RequiresResolution
)

// Transition is one a workflow offers. It is offered to an item in a status
// of From, or to an item in any other status than To when From is empty.
type Transition struct {
	ID, Name string
	From     []string
	To       string
	// Resolution is what the screen holds of the resolution.
	Resolution Screen
	// Asks names fields of the screen that are required besides, such as a
	// workflow's "Reviewer".
	Asks []string
	// Refuses makes the workflow refuse the transition with this message,
	// as a validator does.
	Refuses string
}

// Workflow is a project's: its statuses and transitions, and the
// resolutions the site has.
type Workflow struct {
	Statuses    []Status
	Transitions []Transition
	// Resolutions are the names of the site's resolutions.
	Resolutions []string
}

// Classic is the workflow of a standard Jira Cloud project: To Do, In
// Progress and Done, a transition into Done that takes a resolution, and
// from Done only a way back to To Do (no transition into another status of
// category Done).
func Classic() Workflow {
	return Workflow{
		Statuses: []Status{{StatusToDo, CategoryNew}, {StatusInProgress, CategoryIndeterminate}, {StatusDone, CategoryDone}},
		Transitions: []Transition{
			{ID: "11", Name: "Start", From: []string{StatusToDo}, To: StatusInProgress},
			{ID: "21", Name: "Done", To: StatusDone, Resolution: TakesResolution},
			{ID: "31", Name: "Reopen", From: []string{StatusDone}, To: StatusToDo},
		},
		Resolutions: []string{ResolutionDone, ResolutionWontDo, "Duplicate", "Cannot Reproduce"},
	}
}

// TeamManaged is the workflow of a team-managed project: the same statuses,
// and no resolution to send.
func TeamManaged() Workflow {
	w := Classic()
	w.Transitions = slices.Clone(w.Transitions)
	w.Transitions[1].Resolution = NoScreen
	w.Resolutions = nil
	return w
}

// category is the category of the status named name.
func (w Workflow) category(name string) string {
	for _, s := range w.Statuses {
		if s.Name == name {
			return s.Category
		}
	}
	return ""
}

// offered are the transitions an item in status is offered.
func (w Workflow) offered(status string) []Transition {
	var out []Transition
	for _, t := range w.Transitions {
		if len(t.From) == 0 && t.To != status || slices.Contains(t.From, status) {
			out = append(out, t)
		}
	}
	return out
}
