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
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// Imported is what Import makes of a cluster's export: the two model files
// and what it says of them.
type Imported struct {
	// Story and Setup are model/model.yaml and model/setup.yaml.
	Story, Setup []byte
	// Notes say what was left out or changed.
	Notes []string
}

// Import reads a project's objects as kubectl get -o yaml prints them, a
// List or a stream of objects, and returns the model files that hold them
// (docs/plans/no-cluster.md, "One source of truth with the cluster";
// #295): the kinds model/model.yaml holds in it, the kinds model/setup.yaml
// holds, Crews and Agents included, in setup, each sorted by name. What a
// cluster adds is left out: status, and metadata but the name, the labels
// and the annotations no tool wrote (kubectl's and theos.factory.io's are
// dropped). A Crew's paused and pauseRuns are dropped: the pause stopped
// the cluster's Crew for the move, and the lead runtime honours it. Every
// other kind is left out and named in the notes. All objects are one
// project's: one namespace, which is the Project's name. settings is
// model/settings.yaml, empty for none; the files are decoded with it before
// they are returned, so a refusal writes nothing.
func Import(data, settings []byte) (Imported, error) {
	objs, err := readObjects(data)
	if err != nil {
		return Imported{}, err
	}
	var out Imported
	skipped := map[string]int{}
	byKind := map[string][]map[string]any{}
	namespace := ""
	for _, obj := range objs {
		apiVersion, _ := obj["apiVersion"].(string)
		kind, _ := obj["kind"].(string)
		if apiVersion != c4v1beta1.GroupVersion.String() ||
			!slices.Contains(storyKinds, kind) && !slices.Contains(setupKinds, kind) {
			skipped[kind]++
			continue
		}
		meta, _ := obj[keyMetadata].(map[string]any)
		ns, _ := meta["namespace"].(string)
		switch {
		case ns == "":
			return Imported{}, fmt.Errorf("a %s has no metadata.namespace: an export is one project's namespace", kind)
		case namespace == "":
			namespace = ns
		case ns != namespace:
			return Imported{}, fmt.Errorf("objects in namespaces %s and %s: an export is one project's namespace",
				namespace, ns)
		}
		clean, note := importObject(obj)
		if note != "" {
			out.Notes = append(out.Notes, note)
		}
		byKind[kind] = append(byKind[kind], clean)
	}
	if len(byKind["Project"]) == 0 {
		return Imported{}, errors.New("the export holds no Project: export the namespace's projects too " +
			"(kubectl get projects,...)")
	}
	header := "# Imported by theos import from a cluster's namespace " + namespace + ".\n"
	if out.Story, err = importFile(header, byKind, storyKinds); err != nil {
		return Imported{}, err
	}
	if out.Setup, err = importFile(header, byKind, setupKinds); err != nil {
		return Imported{}, err
	}
	for _, kind := range slices.Sorted(maps.Keys(skipped)) {
		out.Notes = append(out.Notes, fmt.Sprintf("left out %d %s: no model file holds the kind",
			skipped[kind], strings.ToLower(cmpKind(kind))))
	}
	if _, err := Decode(out.Story, out.Setup, settings); err != nil {
		return Imported{}, fmt.Errorf("the export does not read as a model (a Crew's Agents, a Container's "+
			"catalog entries and GitProvider are exported with it): %w", err)
	}
	return out, nil
}

// cmpKind names a kind for a note, "object" for one that has none.
func cmpKind(kind string) string {
	if kind == "" {
		return "object"
	}
	return kind
}

// readObjects reads each object of a YAML stream, the items of a List each
// on its own.
func readObjects(data []byte) ([]map[string]any, error) {
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	var objs []map[string]any
	for {
		doc, err := reader.Read()
		if err == io.EOF {
			return objs, nil
		}
		if err != nil {
			return nil, err
		}
		if len(bytes.TrimSpace(stripComments(doc))) == 0 {
			continue
		}
		obj, err := objectOf(doc)
		if err != nil {
			return nil, err
		}
		if kind, _ := obj["kind"].(string); kind == "List" || strings.HasSuffix(kind, "List") {
			items, _ := obj["items"].([]any)
			for _, item := range items {
				if o, ok := item.(map[string]any); ok {
					objs = append(objs, o)
				}
			}
			continue
		}
		objs = append(objs, obj)
	}
}

// importObject is obj as a model file holds it, and a note on what was
// dropped of it beyond what a cluster adds.
func importObject(obj map[string]any) (map[string]any, string) {
	kind, _ := obj["kind"].(string)
	meta, _ := obj[keyMetadata].(map[string]any)
	name, _ := meta[keyName].(string)
	clean := map[string]any{keyName: name}
	for _, key := range []string{"labels", "annotations"} {
		values, _ := meta[key].(map[string]any)
		kept := map[string]any{}
		for k, v := range values {
			if key == "annotations" && (strings.HasPrefix(k, "kubectl.kubernetes.io/") ||
				strings.HasPrefix(k, c4v1beta1.GroupVersion.Group+"/")) {
				continue
			}
			kept[k] = v
		}
		if len(kept) > 0 {
			clean[key] = kept
		}
	}
	out := map[string]any{"apiVersion": obj["apiVersion"], "kind": kind, keyMetadata: clean}
	spec, _ := obj[keySpec].(map[string]any)
	note := ""
	if kind == kindCrew && spec != nil {
		if paused, _ := spec["paused"].(bool); paused {
			note = fmt.Sprintf("Crew %s: paused dropped: the lead runtime works it once the cluster's Crew is gone", name)
		}
		spec = maps.Clone(spec)
		delete(spec, "paused")
		delete(spec, "pauseRuns")
	}
	if spec != nil {
		out[keySpec] = spec
	}
	return out, note
}

// importFile is one model file: header, then each object of kinds, kind by
// kind in kinds' order and by name within a kind.
func importFile(header string, byKind map[string][]map[string]any, kinds []string) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(header)
	for _, kind := range kinds {
		objs := slices.Clone(byKind[kind])
		slices.SortFunc(objs, func(a, b map[string]any) int {
			return strings.Compare(nameOf(a), nameOf(b))
		})
		for _, obj := range objs {
			data, err := yaml.Marshal(obj)
			if err != nil {
				return nil, err
			}
			b.WriteString("---\n")
			b.Write(data)
		}
	}
	return b.Bytes(), nil
}

// nameOf is a cleaned object's name.
func nameOf(obj map[string]any) string {
	meta, _ := obj[keyMetadata].(map[string]any)
	name, _ := meta[keyName].(string)
	return name
}
