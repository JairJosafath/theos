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

// TriageStore is the part of a TaskStore that keeps a Container's Triage
// Tasks, which a Crew reads apart from its other Tasks (#337): asked for by
// interface, as the engine's triage tick needs it.
type TriageStore interface {
	// TriageTasks are the Triage Tasks on container that model.NextTriage
	// reads: every one at work, and those that ended whose issue may be
	// triaged again.
	TriageTasks(ctx context.Context, container string) ([]model.TriageTask, error)
	// StartTriage starts the Triage Task s names on container, triaged by
	// the Agent triager. One of that name that exists already is no error.
	StartTriage(ctx context.Context, container string, s model.TriageStart, triager string) error
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
	// Spec and Triage say the Task's kind, for a store that keeps it in the
	// record: a Spec Task, a Triage Task, or neither, an issue's build.
	Spec, Triage bool `json:",omitempty"`
	// Findings are what the round's approving review and verification left
	// for the merge (#337), kept from their runs until the merge files them.
	Findings *model.Findings `json:",omitempty"`
	// AgentBack is when the agent whose outage ended the round's turn is due
	// back (model.AgentBackAt), zero for none: the Task waits for it, and the
	// turn's next offer forgets it (#336).
	AgentBack time.Time `json:",omitzero"`
}

// Workers runs one role's turn on a Task. The engine offers the turn, reads
// the run back and ends it; whether the work is pushed to a sandbox or
// pulled by a session is the Workers' own. Declared for the Task tick
// (#334); the operator's Sandboxes are behind it since #336.
type Workers interface {
	// Offer offers the turn w names. It is safe to repeat.
	Offer(ctx context.Context, w WorkOrder) error
	// Run reads back the run of role on round of task.
	Run(ctx context.Context, task string, round int, role string) (model.RunState, error)
	// End ends the run of role on round of task, why one of model.EndSilent
	// and model.EndReleased or the engine's own word. It is safe to repeat.
	End(ctx context.Context, task string, round int, role, why string) error
}

// Preparer is the part of Workers that readies a turn before the engine
// offers it, asked for by interface (#336). The operator's Sandboxes have
// it: before an agent gets a sandbox, its credentials and the Container's
// agent access must exist, the Standard must be on main, the issue must be
// open and the test environments the run needs leased; a try that
// continues an earlier one's green commit starts at its review instead.
// Workers whose sessions pull the work have nothing to ready.
//
// A turn the engine restarts (a rerun, a review or verification tried
// again, a conflict to resolve) is offered by a later tick where the
// Workers ready turns, so it is readied as any other: such Workers name
// each turn apart (the operator's Sandbox names count the reruns and
// attempts), so the run a restart ended reads back not started.
type Preparer interface {
	// Prepare readies the turn w names, before the engine saves the start
	// of its run. Not ready, the engine saves nothing and offers nothing
	// this tick: why is the runtime's, which may have changed the Task's
	// record through its store (a wait it says, an issue closed, a start
	// at review), and the engine reads that record back.
	Prepare(ctx context.Context, w WorkOrder) (ready bool, err error)
}

// Suspender is the part of Workers that suspends a turn's run in place,
// asked for by interface (#336): the run keeps what it saved (its
// checkpoint) and goes on from there once resumed (Resumer), with no round
// or rerun spent. The operator's Sandboxes have it: a suspended Sandbox
// keeps its checkpoint volume and loses its pod. Workers whose sessions pull
// the work have none, and a step that would suspend a run waits there,
// saying it is not built.
type Suspender interface {
	// Suspend suspends the run of role on round of task in place, for why.
	// It reports whether it suspended the run afresh: false when an earlier
	// call did (the run counted then, its suspend not yet done), and this one
	// only suspends it again. The engine counts a resume only on a fresh
	// one. Safe to repeat.
	Suspend(ctx context.Context, task string, round int, role string, why Suspension) (fresh bool, err error)
}

// Suspension is why Workers suspend a run (Suspender.Suspend).
type Suspension struct {
	Kind SuspendKind
	// AgentBack is when the agent of a run suspended for it
	// (SuspendForAgent) is due back: the run resumes then.
	AgentBack time.Time
}

// SuspendKind is why a run is suspended in place.
type SuspendKind string

// The kinds of suspend: an interrupted run's, which counts a resume
// (model.SuspendRun); a run's that paused at the runtime's ask; and that of
// a run whose agent was unavailable.
const (
	SuspendInterrupted SuspendKind = "Interrupted"
	SuspendPaused      SuspendKind = "Paused"
	SuspendForAgent    SuspendKind = "ForAgent"
)

// Resumer is the part of Workers that resumes a suspended run in place,
// asked for by interface (#336), with what a new turn of its role would get
// now: the current Agent, and the run's remaining time. Like a Preparer, it
// may hold the resume: the operator's needs the Agent's credentials and the
// run's test environments again before the Sandbox runs.
type Resumer interface {
	// Resume resumes the suspended run of the turn w names. Not ready, it
	// resumes nothing this tick and reports false: why is the runtime's,
	// which may have changed the Task's record through its store, and a
	// later tick asks again.
	Resume(ctx context.Context, w WorkOrder) (resumed bool, err error)
}

// Pauser is the part of Workers that pauses a running turn at the runtime's
// ask (model.TaskFacts.Pause, a Crew's or Brief's pause), asked for by
// interface (#336). The engine brings the run's pause up to date on every
// tick of a phase with a run, before it carries out the step: a run that
// works is asked to pause at its next tool boundary, or the ask is taken
// back, and a suspended run whose resume a pause holds is kept resumable.
// Workers with no Pauser pause nothing.
type Pauser interface {
	// AskPause asks the running run of role on round of task to pause for
	// why at its next tool boundary; an empty why takes the ask back. Safe
	// to repeat.
	AskPause(ctx context.Context, task string, round int, role, why string) error
	// HoldSuspended keeps the suspended run of role on round of task
	// resumable while a pause holds its resume: the operator takes its
	// Sandbox's shutdown time off, which would expire it and its checkpoint.
	// Safe to repeat.
	HoldSuspended(ctx context.Context, task string, round int, role string) error
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
