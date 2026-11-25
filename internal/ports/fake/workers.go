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

package fake

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// Workers is a ports.Workers in memory, the way a runtime whose sessions
// pull the work behaves: an offered turn runs until the test says what its
// session did (SetRun), and an end of a turn whose claim ran out reads back
// interrupted until it is offered again.
type Workers struct {
	mu     sync.Mutex
	turns  map[string]*turn
	offers []ports.WorkOrder
	ends   []End
}

// End is a turn the engine ended, and why.
type End struct {
	Task  string
	Round int
	Role  string
	Why   string
}

type turn struct {
	open bool
	run  model.RunState
}

var _ ports.Workers = (*Workers)(nil)

// NewWorkers returns Workers with no turn offered.
func NewWorkers() *Workers { return &Workers{turns: map[string]*turn{}} }

func turnKey(task string, round int, role string) string {
	return fmt.Sprintf("%s/%d/%s", task, round, role)
}

// Offer offers w's turn, started and unclaimed; a turn offered and not
// ended since is left as it is.
func (w *Workers) Offer(_ context.Context, o ports.WorkOrder) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := turnKey(o.Task, o.Round, o.Role)
	if t := w.turns[key]; t != nil && t.open {
		return nil
	}
	w.turns[key] = &turn{open: true, run: model.RunState{Started: true,
		Markers: &model.MarkerRun{Unclaimed: true}, Config: model.RunConfig{Agent: o.Agent}}}
	w.offers = append(w.offers, o)
	return nil
}

// Run is the turn's run: not started before it is offered.
func (w *Workers) Run(_ context.Context, task string, round int, role string) (model.RunState, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t := w.turns[turnKey(task, round, role)]; t != nil {
		return t.run, nil
	}
	return model.RunState{}, nil
}

// End ends the turn. One ended for its claim (model.EndSilent,
// model.EndReleased) reads back interrupted; any other is gone.
func (w *Workers) End(_ context.Context, task string, round int, role, why string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.ends = append(w.ends, End{Task: task, Round: round, Role: role, Why: why})
	t := w.turns[turnKey(task, round, role)]
	if t == nil {
		return nil
	}
	t.open = false
	if why == model.EndSilent || why == model.EndReleased {
		t.run = model.RunState{Started: true, Done: true, Interrupted: true,
			Failure: "the run ended with no answer: " + why}
		return nil
	}
	delete(w.turns, turnKey(task, round, role))
	return nil
}

// SetRun says what the session of an offered turn did: claimed it,
// answered it or let its claim run out.
func (w *Workers) SetRun(task string, round int, role string, run model.RunState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	key := turnKey(task, round, role)
	t := w.turns[key]
	if t == nil {
		t = &turn{open: true}
		w.turns[key] = t
	}
	t.run = run
}

// Offers are the turns offered, in order, repeats not included.
func (w *Workers) Offers() []ports.WorkOrder {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.offers)
}

// Ends are the turns ended, in order.
func (w *Workers) Ends() []End {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.ends)
}
