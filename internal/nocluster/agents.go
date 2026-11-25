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

package nocluster

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/harness"
	"github.com/JairJosafath/theos/internal/harness/claudecode"
	"github.com/JairJosafath/theos/internal/method"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
)

// LeadProject is m as the lead's files say it (docs/plans/lead-runtime.md,
// decision 7): each Crew, sorted by name, with the Agents it names for the
// roles the lead runtime plays (its implementer and reviewer, then its
// routes'), each once, and what the lead runtime does not honour of it.
func LeadProject(m *Model) method.Project {
	p := method.Project{Name: m.Project}
	if len(m.Objects.GitProviders) > 0 {
		p.Host = string(m.Objects.GitProviders[0].Spec.Provider)
	}
	crews := slices.Clone(m.Crews)
	slices.SortFunc(crews, func(a, b c4v1beta1.Crew) int { return strings.Compare(a.Name, b.Name) })
	for i := range crews {
		c := &crews[i]
		crew := method.Crew{Name: c.Name, Container: c.Spec.ContainerRef.Name, Parallel: int(c.Spec.Parallel),
			Notes: model.NotHonoured(CrewSetup(m, c), model.LeadCapabilities())}
		add := func(role string, ref *c4v1beta1.LocalRef) {
			if ref == nil || slices.ContainsFunc(crew.Agents, func(a method.Agent) bool {
				return a.Name == ref.Name && a.Role == role
			}) {
				return
			}
			a := method.Agent{Name: ref.Name, Role: role}
			if agent := modelfacts.Find(m.Agents, ref.Name); agent != nil {
				a.Model, a.Description = agent.Spec.Model, agent.Spec.Description
			}
			crew.Agents = append(crew.Agents, a)
		}
		add(model.RoleImplementer, &c.Spec.Implementer)
		add(model.RoleReviewer, &c.Spec.Reviewer)
		for _, r := range c.Spec.Routes {
			add(model.RoleImplementer, r.Implementer)
			add(model.RoleReviewer, r.Reviewer)
		}
		p.Crews = append(p.Crews, crew)
	}
	return p
}

// leadHarness is the harness whose files theos agents writes: Claude Code,
// the one built (decision 7; other harnesses are #338's).
var leadHarness harness.LeadFiles = claudecode.Harness{}

// AgentFiles are the lead's files for m, as the harness renders them.
func AgentFiles(m *Model) ([]harness.File, error) {
	if len(m.Crews) == 0 {
		return nil, fmt.Errorf("%s holds no Crew: the lead hands out a Crew's work, so add one first", SetupFile)
	}
	return leadHarness.Render(LeadProject(m))
}

// Agents writes the lead's files into the project repository, fsys read
// and write writing it, and reports each: written, or unchanged. A second
// run on the same model writes nothing.
func Agents(out io.Writer, m *Model, fsys fs.FS, write func(path string, data []byte) error) error {
	files, err := AgentFiles(m)
	if err != nil {
		return err
	}
	for _, f := range files {
		got, err := fs.ReadFile(fsys, f.Path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err == nil && bytes.Equal(got, f.Data) {
			if _, err := fmt.Fprintf(out, "%s: unchanged\n", f.Path); err != nil {
				return err
			}
			continue
		}
		if err := write(f.Path, f.Data); err != nil {
			return fmt.Errorf("writing %s: %w", f.Path, err)
		}
		if _, err := fmt.Fprintf(out, "%s: written\n", f.Path); err != nil {
			return err
		}
	}
	return nil
}

// agentDrift prints each of the lead's files fsys lacks or holds otherwise
// than theos agents writes it. A model with no Crew has none to check.
func agentDrift(out io.Writer, m *Model, fsys fs.FS) error {
	if fsys == nil || len(m.Crews) == 0 {
		return nil
	}
	files, err := AgentFiles(m)
	if err != nil {
		return err
	}
	drifts, err := harness.Drifted(fsys, files)
	if err != nil {
		return err
	}
	for _, d := range drifts {
		says := "changed since theos agents wrote it; theos agents writes it again"
		if d.Missing {
			says = "not written; theos agents writes it"
		}
		if _, err := fmt.Fprintf(out, "%s: %s\n", d.Path, says); err != nil {
			return err
		}
	}
	return nil
}
