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
	"slices"
	"strings"
)

// What a runtime honours of a Crew and its Agents
// (docs/plans/lead-runtime.md, decision 6). Each field of CrewSpec and
// AgentSpec has a row in CrewFields or AgentFields: honoured by every
// runtime, or the reason word a runtime that lacks the capability gives.
// Nothing is dropped silently: a runtime's status names each field it
// doesn't honour.

// The reason words for a field a runtime does not honour.
const (
	// ReasonNoSandbox: the runtime makes no sandbox, so what shapes one
	// (image, resources, env, credentials, timeout) is not read.
	ReasonNoSandbox = "NoSandbox"
	// ReasonNoScheduler: the runtime keeps no clock between its ticks, so
	// no schedule fires and no release is cut, tagged or published.
	ReasonNoScheduler = "NoScheduler"
	// ReasonCostUnreported: the runtime doesn't read what a run cost, so no
	// budget or token limit stops one.
	ReasonCostUnreported = "CostUnreported"
	// ReasonRoleNotBuilt: the runtime plays the implementer and the
	// reviewer only.
	ReasonRoleNotBuilt = "RoleNotBuilt"
	// ReasonHarnessNotBuilt: the runtime writes no files for the Agent's
	// harness; the harness gets the work tools over MCP only.
	ReasonHarnessNotBuilt = "HarnessNotBuilt"
	// ReasonPersonMerges: the runtime merges nothing; a person merges each
	// approved pull request.
	ReasonPersonMerges = "PersonMerges"
)

// Capabilities is what a runtime does of what a Crew and its Agents ask. A
// capability left false is not honoured, with its reason word.
type Capabilities struct {
	// Merges means the runtime merges an approved Task (else PersonMerges).
	Merges bool
	// Sandboxes means each run is a sandbox the runtime makes from the
	// Agent's image, resources, env, credentials and timeout (else NoSandbox).
	Sandboxes bool
	// Schedules means the runtime keeps a clock of its own: schedules and
	// releases (else NoScheduler).
	Schedules bool
	// Costs means the runtime reads what a run cost (else CostUnreported).
	Costs bool
	// AllRoles means the triager, spec writer and verifier play their roles
	// (else RoleNotBuilt).
	AllRoles bool
	// Harnesses are the harnesses the runtime builds; empty means every one
	// the API takes. Another is HarnessNotBuilt.
	Harnesses []string
}

// OperatorCapabilities are the operator's: everything.
func OperatorCapabilities() Capabilities {
	return Capabilities{Merges: true, Sandboxes: true, Schedules: true, Costs: true, AllRoles: true}
}

// LeadCapabilities are the lead runtime's, a Claude Code session that
// drives theos (decision 6's table): the implementer and the reviewer on
// claude-code, a person merging.
func LeadCapabilities() Capabilities {
	return Capabilities{Harnesses: []string{HarnessClaudeCode}}
}

// HarnessClaudeCode is Agent.spec.harness for Claude Code.
const HarnessClaudeCode = "claude-code"

// honours is whether caps honours a field whose row is reason.
func (caps Capabilities) honours(reason string) bool {
	switch reason {
	case "":
		return true
	case ReasonNoSandbox:
		return caps.Sandboxes
	case ReasonNoScheduler:
		return caps.Schedules
	case ReasonCostUnreported:
		return caps.Costs
	case ReasonRoleNotBuilt:
		return caps.AllRoles
	case ReasonPersonMerges:
		return caps.Merges
	}
	return false
}

// harness is whether caps builds harness.
func (caps Capabilities) harness(harness string) bool {
	return len(caps.Harnesses) == 0 || slices.Contains(caps.Harnesses, harness)
}

// The JSON names of the fields every kind's spec may have.
const (
	fieldDisplayName = "displayName"
	fieldDescription = "description"
)

// CrewFields is the row of each field of CrewSpec, by its JSON name: "" for
// honoured by every runtime, else the reason a runtime without the
// capability gives.
var CrewFields = map[string]string{
	"containerRef":    "",
	fieldDisplayName:  "",
	fieldDescription:  "",
	"implementer":     "",
	"reviewer":        "",
	"routes":          "",
	"parallel":        "",
	"paused":          "",
	"pauseRuns":       ReasonNoSandbox,
	"triager":         ReasonRoleNotBuilt,
	"specWriter":      ReasonRoleNotBuilt,
	"specApproval":    ReasonRoleNotBuilt,
	"verifier":        ReasonRoleNotBuilt,
	"schedules":       ReasonNoScheduler,
	"tagReleases":     ReasonNoScheduler,
	"prepareReleases": ReasonNoScheduler,
	"releaseCadence":  ReasonNoScheduler,
	"publishReleases": ReasonNoScheduler,
	"approval":        ReasonPersonMerges,
	"taskBudgetUSD":   ReasonCostUnreported,
}

// AgentFields is the row of each field of AgentSpec, as CrewFields. The
// harness's row is HarnessNotBuilt, given for a harness the runtime
// doesn't build.
var AgentFields = map[string]string{
	fieldDisplayName:       "",
	fieldDescription:       "",
	"model":                "",
	"maxRounds":            "",
	"harness":              ReasonHarnessNotBuilt,
	"sessionTokenLimit":    ReasonCostUnreported,
	"image":                ReasonNoSandbox,
	"resources":            ReasonNoSandbox,
	"env":                  ReasonNoSandbox,
	"credentialsSecretRef": ReasonNoSandbox,
	"timeoutMinutes":       ReasonNoSandbox,
}

// CrewSetup is a Crew as a runtime reads it: the spec fields it sets, and
// the Agents it names, each once.
type CrewSetup struct {
	Name   string
	Fields []string
	Agents []AgentSetup
}

// AgentSetup is an Agent as a runtime reads it: the spec fields it sets and
// its harness.
type AgentSetup struct {
	Name    string
	Fields  []string
	Harness string
}

// FieldNote is a field a runtime does not honour, and why. Object is
// "Crew <name>" or "Agent <name>"; Field is spec.<field>, or "" for a note
// on the Crew as a whole.
type FieldNote struct {
	Object string
	Field  string
	Reason string
	Says   string
}

// NotHonoured lists each field c and its Agents set that a runtime with
// caps does not honour, the Crew's first, each in its spec's order of
// names; and, where the runtime merges nothing, that a person merges. A
// field with no row is not honoured either (ReasonUnknownField), so a field
// added to the API is never dropped silently. An Agent's harness is read
// whether it is written or defaulted.
func NotHonoured(c CrewSetup, caps Capabilities) []FieldNote {
	var notes []FieldNote
	crew := "Crew " + c.Name
	if !caps.Merges {
		notes = append(notes, FieldNote{Object: crew, Reason: ReasonPersonMerges, Says: says(ReasonPersonMerges)})
	}
	notes = append(notes, fieldNotes(crew, c.Fields, CrewFields, caps)...)
	for _, a := range c.Agents {
		agent := "Agent " + a.Name
		if a.Harness != "" && !caps.harness(a.Harness) {
			notes = append(notes, FieldNote{Object: agent, Field: "spec.harness", Reason: ReasonHarnessNotBuilt,
				Says: says(ReasonHarnessNotBuilt)})
		}
		notes = append(notes, fieldNotes(agent, a.Fields, AgentFields, caps)...)
	}
	return notes
}

// ReasonUnknownField is the reason of a field the tables have no row for.
const ReasonUnknownField = "UnknownField"

func fieldNotes(object string, fields []string, rows map[string]string, caps Capabilities) []FieldNote {
	sorted := slices.Clone(fields)
	slices.Sort(sorted)
	notes := make([]FieldNote, 0, len(sorted))
	for _, f := range slices.Compact(sorted) {
		reason, ok := rows[f]
		switch {
		case !ok:
			reason = ReasonUnknownField
		case reason == ReasonHarnessNotBuilt, caps.honours(reason):
			continue
		}
		notes = append(notes, FieldNote{Object: object, Field: "spec." + f, Reason: reason, Says: says(reason)})
	}
	return notes
}

// says is what a reason tells a person.
func says(reason string) string {
	switch reason {
	case ReasonNoSandbox:
		return "this runtime makes no sandbox; the session's own machine runs the role"
	case ReasonNoScheduler:
		return "this runtime keeps no clock between commands: no schedule fires and no release is cut, " +
			"tagged or published"
	case ReasonCostUnreported:
		return "this runtime doesn't read what a run costs: no budget or token limit stops one"
	case ReasonRoleNotBuilt:
		return "this runtime plays the implementer and the reviewer only (#337)"
	case ReasonHarnessNotBuilt:
		return "this runtime writes no files for this harness; it gets the work tools over MCP only"
	case ReasonPersonMerges:
		return "this runtime merges nothing: a person merges each approved pull request (theos merge)"
	}
	return "theos has no row for this field yet; it is not read"
}

// FieldNoteLine is a note as theos status prints it.
func FieldNoteLine(n FieldNote) string {
	parts := []string{n.Object}
	if n.Field != "" {
		parts = append(parts, n.Field)
	}
	return strings.Join(append(parts, n.Reason, n.Says), ": ")
}
