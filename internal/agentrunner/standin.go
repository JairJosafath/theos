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

package agentrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
)

// FakeAgent stands in for a real agent in tests and demos. Implementing, it adds
// a line to STAND-IN.md; reviewing, it approves, or first asks for changes, and
// may file a follow-up; verifying, it reads STAND-IN.md and judges from it and
// the issue's text (model.StandInVerify); planning, it plans from the catalog
// alone (model.StandInPlan); triaging, it decides from the issue's text
// (model.StandInTriage); writing a spec, it writes model.StandInSpec.
type FakeAgent struct {
	Role  string
	Round int
	// Goal is the job's goal: for a triage, the issue's title and body.
	Goal string
	// Issue and Spec are a Spec Task's issue and spec file: implementing, the
	// agent writes the spec there.
	Issue int
	Spec  string
	// ChangesFirst makes the review ask for changes in round 1.
	ChangesFirst bool
	// NoChange makes implementing change nothing.
	NoChange bool
	// FollowUp makes the approval file one follow-up with acceptance criteria.
	FollowUp bool
	// Open makes the approval accept one criterion as left open, for a person.
	Open bool
	// Criteria are a judge run's criteria: the stand-in judge passes each.
	Criteria []string
}

// standInRisk in an issue's text has the stand-in reviewer call its change
// high-risk, as it has the stand-in triager label the issue
// (model.StandInTriage).
const standInRisk = "stand-in: risk high"

// standInNothing in an issue's text has the stand-in implementer find
// nothing to do: it changes nothing and says so (#72).
const standInNothing = "stand-in: nothing to do"

// Run does the scripted work.
func (a FakeAgent) Run(_ context.Context, dir, _ string) (string, error) {
	if a.Role == RolePlan {
		return a.plan(dir)
	}
	if a.Role == RoleScore {
		return "Read the change.\n" + model.StandInScore(a.Criteria), nil
	}
	if a.Role == RoleTriage {
		decision, err := json.Marshal(model.StandInTriage(a.Goal))
		return "Read the issue.\n" + string(decision), err
	}
	if a.Role == RoleVerify {
		return a.verify(dir)
	}
	if a.Role == RoleReview {
		if a.ChangesFirst && a.Round == 1 {
			return `{"verdict": "changes", "summary": "Stand-in review: add a second line."}`, nil
		}
		if a.FollowUp {
			return `Looked at it.` + "\n" + `{"verdict": "approve", "summary": "Stand-in review: fine.", ` +
				`"followUps": [{"title": "Stand-in follow-up: add a third line", ` +
				`"body": "The stand-in reviewer files this to show a follow-up.", ` +
				`"criteria": ["STAND-IN.md has a third line", "make check passes"]}]}`, nil
		}
		if a.Open {
			return `Looked at it.` + "\n" + `{"verdict": "approve", "summary": "Stand-in review: fine, one criterion open.", ` +
				`"open": [{"criterion": "A screenshot of the editor", "why": "no desktop in the sandbox"}]}`, nil
		}
		if strings.Contains(strings.ToLower(a.Goal), standInRisk) {
			return `Looked at it.` + "\n" + `{"verdict": "approve", "summary": "Stand-in review: fine.", ` +
				`"risk": "high", "riskReason": "Stand-in review: the issue asks to be called high-risk."}`, nil
		}
		return `Looked at it.` + "\n" + `{"verdict": "approve", "summary": "Stand-in review: fine."}`, nil
	}
	if a.NoChange {
		return "Nothing to do.", nil
	}
	if a.Role == RoleImplement && a.Spec == "" && strings.Contains(strings.ToLower(a.Goal), standInNothing) {
		return "Looked at it.\n" + model.NothingPrefix + " the stand-in was asked to find nothing.", nil
	}
	if a.Spec != "" {
		return a.writeSpec(dir)
	}
	f, err := os.OpenFile(filepath.Join(dir, "STAND-IN.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644) //nolint:gosec // in the checkout
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	if _, err := fmt.Fprintf(f, "Round %d of the stand-in agent.\n", a.Round); err != nil {
		return "", err
	}
	return fmt.Sprintf("Added round %d to STAND-IN.md.", a.Round), nil
}

// verify reads STAND-IN.md, as the stand-in's way to run the change, and
// reports model.StandInVerify's verdict on it.
func (a FakeAgent) verify(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "STAND-IN.md")) //nolint:gosec // in the checkout
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	verdict, summary := model.StandInVerify(a.Goal, a.Round, string(data))
	report, err := json.Marshal(map[string]string{"verdict": verdict, "summary": summary})
	return "Ran cat STAND-IN.md.\n" + string(report), err
}

// writeSpec writes the stand-in spec of the issue in its spec file.
func (a FakeAgent) writeSpec(dir string) (string, error) {
	file := filepath.Join(dir, filepath.FromSlash(a.Spec))
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil { //nolint:gosec // in the checkout
		return "", err
	}
	if err := os.WriteFile(file, []byte(model.StandInSpec(a.Issue, a.Goal)), 0o644); err != nil { //nolint:gosec // in the checkout
		return "", err
	}
	return fmt.Sprintf("Wrote the stand-in spec in %s.", a.Spec), nil
}

// plan writes the stand-in's plan for the workspace the operator wrote on main.
func (a FakeAgent) plan(dir string) (string, error) {
	workspace := map[string]string{}
	for _, name := range model.WorkspaceFiles {
		data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name))) //nolint:gosec // the workspace's fixed names
		if err != nil {
			return "", err
		}
		workspace[name] = string(data)
	}
	plan, err := model.StandInPlan(workspace)
	if err != nil {
		return "", err
	}
	for name, data := range plan {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("Round %d: the stand-in planner planned from the catalog alone.", a.Round), nil
}
