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

package ports

import (
	"context"
	"time"

	"github.com/JairJosafath/theos/internal/model"
)

// The runtime's ports (docs/plans/lead-runtime.md, decision 3): where a
// Container's Tasks are kept and who runs a role's turn on one. The operator
// keeps Tasks as objects and runs Sandboxes; the lead runtime keeps them on
// the Git host, a Task's branch and its issue's markers, and its sessions
// pull the work. internal/engine calls both the same way.

// TaskStore keeps a Container's Tasks.
type TaskStore interface {
	// Tasks are the Tasks on container a Crew counts: every one but a
	// Triage Task, which the Crew reads apart. A Task just started is among
	// them.
	Tasks(ctx context.Context, container string) ([]model.CrewTask, error)
	// Start starts the Task s names on container. A Task of that name that
	// exists already is no error, and is left as it is.
	Start(ctx context.Context, container string, s model.IssueStart) error
	// Progress is the record of the Task named task; a Task the store
	// doesn't hold is an error. A Task just started is Pending, round 0.
	Progress(ctx context.Context, task string) (TaskRecord, error)
	// SaveProgress keeps r as the Task's record. A final phase ends the
	// Task: Tasks lists it Final, and Failed for model.TaskFailed.
	SaveProgress(ctx context.Context, task string, r TaskRecord) error
}

// TaskRecord is what a TaskStore keeps of a Task's progress: what its
// transitions change, its pull request and the round's summary, when its
// phase last changed, and, once it ended, why.
type TaskRecord struct {
	Progress       model.TaskProgress
	PullRequest    int
	PullRequestURL string
	Summary        string
	PhaseSince     time.Time
	Reason         string
	Message        string
}

// Workers runs one role's turn on a Task. The engine offers the turn, reads
// the run back and ends it; whether the work is pushed to a sandbox or
// pulled by a session is the Workers' own. Declared for the Task tick
// (#334); the operator's Sandboxes come behind it with #336.
type Workers interface {
	// Offer offers the turn w names. It is safe to repeat.
	Offer(ctx context.Context, w WorkOrder) error
	// Run reads back the run of role on round of task.
	Run(ctx context.Context, task string, round int, role string) (model.RunState, error)
	// End ends the run of role on round of task, why one of model.EndSilent
	// and model.EndReleased or the engine's own word. It is safe to repeat.
	End(ctx context.Context, task string, round int, role, why string) error
}

// WorkOrder is the turn Workers.Offer offers.
type WorkOrder = model.WorkOrder

// Capabilities is what a runtime does of what a Crew and its Agents ask
// (docs/plans/lead-runtime.md, decision 6): model.NotHonoured names each
// field a runtime without one does not honour, with its reason word.
type Capabilities = model.Capabilities

// Runtime is one runtime: where its Tasks are kept, who runs their turns,
// and what it can do.
type Runtime interface {
	Store() TaskStore
	Workers() Workers
	Capabilities() Capabilities
}
