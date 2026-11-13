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

package jira

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/JairJosafath/theos/internal/ports"
)

// transition is a transition an item offers (Atlassian's "Get transitions"
// with transitions.fields expanded): the status it leads to and, when it has
// a screen, the fields the screen takes.
type transition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	IsAvailable *bool  `json:"isAvailable"`
	To          struct {
		Name           string `json:"name"`
		StatusCategory struct {
			Key string `json:"key"`
		} `json:"statusCategory"`
	} `json:"to"`
	Fields map[string]screenField `json:"fields"`
}

// screenField is a field of a transition's screen.
type screenField struct {
	Name            string            `json:"name"`
	Required        bool              `json:"required"`
	HasDefaultValue bool              `json:"hasDefaultValue"`
	AllowedValues   []json.RawMessage `json:"allowedValues"`
}

// allowed are the names of the values the field takes: a resolution is an
// object with a name, an option a string or an object with a value.
func (f screenField) allowed() []string {
	var names []string
	for _, raw := range f.AllowedValues {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			names = append(names, s)
			continue
		}
		var o struct{ Name, Value string }
		if json.Unmarshal(raw, &o) == nil {
			names = append(names, cmp.Or(o.Name, o.Value))
		}
	}
	return names
}

// cannotClose is the failure of an item no transition closes: the factory
// still comments, and a person closes it.
func (j *jira) cannotClose(number int, format string, args ...any) *ports.IssuesError {
	return &ports.IssuesError{Reason: ports.ReasonCannotClose,
		Message: fmt.Sprintf("jira: cannot close %s: %s", j.key(number), fmt.Sprintf(format, args...))}
}

// outcome is how an item is closed for reason, with the resolution's
// default filled in.
func (j *jira) outcome(reason ports.CloseReason) (Outcome, bool) {
	var o Outcome
	switch reason {
	case ports.CloseCompleted:
		o = j.closing.Completed
		if o.Resolution == "" {
			o.Resolution = resolutionDone
		}
	case ports.CloseNotPlanned:
		o = j.closing.NotPlanned
		if o.Resolution == "" {
			o.Resolution = resolutionWontDo
		}
	default:
		return Outcome{}, false
	}
	return o, true
}

// CloseIssue closes an item with a transition it offers into a status of
// category Done (decision 17), with a resolution where the transition's
// screen takes one. An item that is already closed stays as it is: Jira is
// not asked to reopen it, since closed is what was asked for. An item no
// transition closes is a CannotClose, with the names the site does offer.
func (j *jira) CloseIssue(ctx context.Context, number int, reason ports.CloseReason) error {
	want, ok := j.outcome(reason)
	if !ok {
		return &ports.IssuesError{Reason: ports.ReasonTrackerError,
			Message: fmt.Sprintf("jira: cannot close %s: %q is not a reason to close an item", j.key(number), reason)}
	}
	n, err := j.item(ctx, number, "project,status")
	if err != nil {
		return err
	}
	if s := n.Fields.Status; s != nil && s.StatusCategory.Key == categoryDone {
		return nil
	}
	var offered struct {
		Transitions []transition `json:"transitions"`
	}
	if err := j.get(ctx, "read the transitions of "+j.key(number), "/issue/"+url.PathEscape(n.ID)+"/transitions",
		url.Values{"expand": {"transitions.fields"}}, ports.ReasonIssueNotFound, &offered); err != nil {
		return err
	}
	t, err := j.pick(number, offered.Transitions, want)
	if err != nil {
		return err
	}
	body := map[string]any{"transition": map[string]any{"id": t.ID}}
	if _, takes := t.Fields[fieldResolution]; takes {
		body["fields"] = map[string]any{fieldResolution: map[string]any{"name": want.Resolution}}
	}
	status, data, err := j.do(ctx, "close "+j.key(number), http.MethodPost,
		"/issue/"+url.PathEscape(n.ID)+"/transitions", nil, body)
	if err != nil {
		return err
	}
	if status == http.StatusBadRequest {
		// A condition, a validator or a required field the screen didn't
		// show: the workflow refuses, in Jira's words.
		return j.cannotClose(number, "Jira refused the transition %q: %s", t.Name, oneLine(words(data), maxMessage))
	}
	return j.read("close "+j.key(number), status, data, ports.ReasonIssueNotFound, nil)
}

// pick is the transition that closes the item the way want says, or the
// reason none does.
func (j *jira) pick(number int, offered []transition, want Outcome) (transition, error) {
	var done []transition
	var reachable []string
	for _, t := range offered {
		if t.IsAvailable != nil && !*t.IsAvailable {
			continue
		}
		if t.To.StatusCategory.Key == categoryDone {
			done = append(done, t)
			reachable = append(reachable, quoted(t.To.Name))
		}
	}
	if len(done) == 0 {
		all := make([]string, 0, len(offered))
		for _, t := range offered {
			all = append(all, quoted(t.To.Name))
		}
		return transition{}, j.cannotClose(number, "its workflow offers no transition into a status of category Done"+
			" (it offers %s)", phrase(unique(all)))
	}
	chosen := done[0]
	if want.Status != "" {
		i := -1
		for k, t := range done {
			if strings.EqualFold(t.To.Name, want.Status) {
				i = k
				break
			}
		}
		if i < 0 {
			return transition{}, j.cannotClose(number, "it cannot go to the status %q; the statuses of category Done "+
				"it offers are %s", want.Status, phrase(unique(reachable)))
		}
		chosen = done[i]
	}
	return chosen, j.screen(number, chosen, want)
}

// screen holds the transition's screen against what is sent: a resolution
// the site has, and nothing else required that the factory can't fill.
func (j *jira) screen(number int, t transition, want Outcome) error {
	for _, key := range slices.Sorted(maps.Keys(t.Fields)) {
		if f := t.Fields[key]; key != fieldResolution && f.Required && !f.HasDefaultValue {
			return j.cannotClose(number, "the transition %q asks for the field %q, which the factory does not fill",
				t.Name, f.Name)
		}
	}
	f, takes := t.Fields[fieldResolution]
	if !takes {
		return nil
	}
	have := f.allowed()
	if len(have) == 0 {
		return nil
	}
	for _, name := range have {
		if strings.EqualFold(name, want.Resolution) {
			return nil
		}
	}
	quotedNames := make([]string, 0, len(have))
	for _, name := range have {
		quotedNames = append(quotedNames, quoted(name))
	}
	return j.cannotClose(number, "the site has no resolution %q for the transition %q; it has %s", want.Resolution,
		t.Name, phrase(quotedNames))
}

func quoted(s string) string { return fmt.Sprintf("%q", s) }

// phrase is names as one phrase.
func phrase(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// unique is s without its repeats, in order.
func unique(s []string) []string {
	var out []string
	for _, v := range s {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
