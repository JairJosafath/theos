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
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
)

// The kinds and keys export and import name.
const (
	kindContainer = "Container"
	kindCrew      = "Crew"
	keyMetadata   = "metadata"
	keySpec       = "spec"
	keyName       = "name"
)

// personApproval is the approval of a Crew that holds every change for a
// person, as docs/adopt.md's model has it.
var personApproval = []map[string]string{{"require": string(c4v1beta1.ApprovalPerson)}}

// Export writes to w the project's two model files, read from fsys, as one
// stream a cluster applies (docs/plans/no-cluster.md, "One source of truth
// with the cluster"; #295): every object in the Project's namespace;
// adopt: true on every Container whose repository has commits, so the
// operator takes it as it is and scaffolds nothing (adopt is immutable, so
// it is set before the Container reaches the cluster); and every Crew
// holding every change for a person, approval Person and, with a spec
// writer, specApproval Person, as docs/adopt.md's model does. A Container
// with a repository and no Crew gets one, on init's Agents. Each document
// keeps its comments and every field it writes; a document of comments
// alone is left out. env.Out says what was changed.
func Export(ctx context.Context, env Env, fsys fs.FS, w io.Writer) error {
	story, err := fs.ReadFile(fsys, ModelFile)
	if err != nil {
		return err
	}
	setup, err := fs.ReadFile(fsys, SetupFile)
	if err != nil {
		return err
	}
	settings, err := fs.ReadFile(fsys, SettingsFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	m, err := Decode(story, setup, settings)
	if err != nil {
		return err
	}
	cs, err := containers(ctx, env, m)
	if err != nil {
		return err
	}
	scaffolded := map[string]bool{}
	for _, c := range cs {
		commits, err := c.host.History(ctx, c.host.Repository(c.obj.Name), 1)
		if err != nil && !errors.Is(err, ports.ErrRepositoryNotFound) {
			return fmt.Errorf("container %s: %w", c.obj.Name, err)
		}
		scaffolded[c.obj.Name] = len(commits) > 0
	}
	out, notes, err := exportStream(m, splitDocuments(story), splitDocuments(setup), scaffolded)
	if err != nil {
		return err
	}
	if _, err := w.Write(out); err != nil {
		return err
	}
	for _, n := range notes {
		if _, err := fmt.Fprintln(env.Out, n); err != nil {
			return err
		}
	}
	return nil
}

// exportStream is the stream Export writes, and its notes; scaffolded says,
// by Container, whether its repository has commits.
func exportStream(m *Model, story, setup []document, scaffolded map[string]bool) ([]byte, []string, error) {
	var out bytes.Buffer
	fmt.Fprintf(&out, "# Project %s, exported by theos export from %s and %s for a cluster.\n"+
		"# Apply it into the namespace %s once its GitProvider's Secret exists.\n", m.Project, ModelFile, SetupFile,
		m.Project)
	var notes []string
	crewed := map[string]bool{}
	for _, c := range m.Crews {
		crewed[c.Spec.ContainerRef.Name] = true
	}
	for _, d := range slices.Concat(story, setup) {
		if d.kind == "" {
			continue
		}
		changes := []change{{[]string{keyMetadata, "namespace"}, m.Project}}
		switch d.kind {
		case kindContainer:
			if c := modelfacts.Find(m.Containers, d.name); c != nil && scaffolded[d.name] && !c.Spec.Adopt {
				changes = append(changes, change{[]string{keySpec, "adopt"}, true})
				notes = append(notes, fmt.Sprintf("Container %s: adopt: true, its repository has commits", d.name))
			}
		case kindCrew:
			changes = append(changes, change{[]string{keySpec, "approval"}, personApproval})
			if c := modelfacts.Find(m.Crews, d.name); c != nil && c.Spec.SpecWriter != nil {
				changes = append(changes, change{[]string{keySpec, "specApproval"}, string(c4v1beta1.SpecApprovalPerson)})
			}
			notes = append(notes, fmt.Sprintf("Crew %s: holds every change for a person (approval: require Person)",
				d.name))
		}
		for _, ch := range changes {
			if err := d.set(ch.path, ch.value); err != nil {
				return nil, nil, fmt.Errorf("%s %s: %w", d.kind, d.name, err)
			}
		}
		out.WriteString("---\n")
		out.WriteString(strings.TrimPrefix(body(d.text), "\n"))
		if !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
			out.WriteString("\n")
		}
	}
	names := make([]string, 0, len(m.Containers))
	for _, c := range m.Containers {
		names = append(names, c.Name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !scaffolded[name] || crewed[name] {
			continue
		}
		crew := name
		if modelfacts.Find(m.Crews, crew) != nil { // a Crew of that name works another Container
			crew += "-crew"
		}
		out.WriteString("---\n# Start small: the Crew holds every change for a person (docs/adopt.md).\n")
		out.Write(crewDocument(m.Project, crew, name))
		var note strings.Builder
		fmt.Fprintf(&note, "Crew %s: added for Container %s, on the Agents %s and %s, holding every change for a "+
			"person", crew, name, agentImplementer, agentReviewer)
		for _, a := range []string{agentImplementer, agentReviewer} {
			if modelfacts.Find(m.Agents, a) == nil {
				fmt.Fprintf(&note, "; %s holds no Agent %s: make it on the cluster", SetupFile, a)
			}
		}
		notes = append(notes, note.String())
	}
	return out.Bytes(), notes, nil
}

// change is a field export sets in a document.
type change struct {
	path  []string
	value any
}

// crewDocument is the Crew name export adds for a Container no Crew works.
func crewDocument(ns, name, container string) []byte {
	return fmt.Appendf(nil, `apiVersion: %s
kind: Crew
metadata:
  name: %s
  namespace: %s
spec:
  containerRef:
    name: %s
  implementer:
    name: %s
  reviewer:
    name: %s
  approval:
  - require: %s
`, c4v1beta1.GroupVersion, name, ns, container, agentImplementer, agentReviewer, c4v1beta1.ApprovalPerson)
}

// encodeNode writes n as YAML with a two-space indent.
func encodeNode(n *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// set sets the field at path in the document to v, keeping its other
// fields and its comments.
func (d *document) set(path []string, v any) error {
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(body(d.text)), &root); err != nil {
		return err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return errors.New("its document is not a mapping")
	}
	m := root.Content[0]
	for _, key := range path[:len(path)-1] {
		m = mappingValue(m, key)
	}
	if err := setKey(m, path[len(path)-1], v); err != nil {
		return err
	}
	data, err := encodeNode(&root)
	if err != nil {
		return err
	}
	sep := ""
	if strings.HasPrefix(d.text, "---") {
		line, _, _ := strings.Cut(d.text, "\n")
		sep = line + "\n"
	}
	d.text = sep + string(data)
	return nil
}
