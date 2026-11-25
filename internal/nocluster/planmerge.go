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
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
)

// MergePlan is model/model.yaml's text story with a checked plan's objects
// merged in, object by object (docs/plans/no-cluster.md, decision 2):
//   - a document the plan leaves alone stays as it is written, byte for byte;
//   - an object the plan changes gets modelfacts.CopyStoryText, as the Brief
//     round's patch, and only the spec fields that changed are written into
//     its document, which keeps every other field (adopt, license,
//     checkCommand, deploy, operations) and its comments;
//   - an object the plan creates is modelfacts.StoryObject's, appended as a
//     new document with the fields it sets.
//
// m is story as Decode read it; created and changed are model.PlanDiff's.
func MergePlan(story []byte, m *Model, created, changed []model.PlanObject) ([]byte, error) {
	docs := splitDocuments(story)
	for _, o := range changed {
		before, err := modelObject(m, o.Ref())
		if err != nil {
			return nil, err
		}
		desired, err := modelfacts.StoryObject(m.Project, o)
		if err != nil {
			return nil, err
		}
		after, ok := before.DeepCopyObject().(modelfacts.APIObject)
		if !ok {
			return nil, fmt.Errorf("copying %s", o.Ref())
		}
		modelfacts.CopyStoryText(after, desired)
		i := slices.IndexFunc(docs, func(d document) bool { return d.kind == o.Kind && d.name == o.Name })
		if i < 0 {
			return nil, fmt.Errorf("%s has no document of %s", ModelFile, o.Ref())
		}
		if err := docs[i].setSpec(before, after); err != nil {
			return nil, fmt.Errorf("%s: %w", o.Ref(), err)
		}
	}
	var out bytes.Buffer
	for _, d := range docs {
		out.WriteString(d.text)
	}
	if out.Len() > 0 && !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
		out.WriteString("\n")
	}
	for _, o := range created {
		doc, err := newDocument(m.Project, o)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", o.Ref(), err)
		}
		out.WriteString("---\n")
		out.Write(doc)
	}
	return out.Bytes(), nil
}

// The YAML tags of the nodes the merge makes.
const (
	tagMap = "!!map"
	tagStr = "!!str"
)

// document is one YAML document of a file as it is written: its text, the
// separator line before it included, and the kind and name it holds.
type document struct {
	text       string
	kind, name string
}

// splitDocuments splits data at the lines the decoder's YAML reader splits
// it at: a line that starts with --- and holds nothing else but a comment.
func splitDocuments(data []byte) []document {
	var docs []document
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			docs = append(docs, newDoc(cur.String()))
			cur.Reset()
		}
	}
	for line := range strings.Lines(string(data)) {
		if rest, ok := strings.CutPrefix(line, "---"); ok {
			if t := strings.TrimSpace(rest); t == "" || strings.HasPrefix(t, "#") {
				flush()
			}
		}
		cur.WriteString(line)
	}
	flush()
	return docs
}

func newDoc(text string) document {
	var head struct {
		Kind     string `yaml:"kind"`
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
	}
	_ = yaml.Unmarshal([]byte(body(text)), &head) // a document Decode read; one that isn't an object has no kind
	return document{text: text, kind: head.Kind, name: head.Metadata.Name}
}

// body is a document's text without its separator line.
func body(text string) string {
	if strings.HasPrefix(text, "---") {
		_, rest, _ := strings.Cut(text, "\n")
		return rest
	}
	return text
}

// setSpec writes into the document each spec field that differs between
// before and after, as the API's JSON spells them, and leaves the rest.
func (d *document) setSpec(before, after modelfacts.APIObject) error {
	was, err := specOf(before)
	if err != nil {
		return err
	}
	is, err := specOf(after)
	if err != nil {
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(body(d.text)), &root); err != nil {
		return err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("its document in %s is not a mapping", ModelFile)
	}
	spec := mappingValue(root.Content[0], "spec")
	keys := slices.Sorted(maps.Keys(is))
	for k := range was {
		if _, ok := is[k]; !ok {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		if reflect.DeepEqual(was[k], is[k]) {
			continue
		}
		if err := setKey(spec, k, is[k]); err != nil {
			return err
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return err
	}
	if err := enc.Close(); err != nil {
		return err
	}
	sep := ""
	if strings.HasPrefix(d.text, "---") {
		line, _, _ := strings.Cut(d.text, "\n")
		sep = line + "\n"
	}
	d.text = sep + buf.String()
	return nil
}

// specOf is obj's spec as the API's JSON spells it.
func specOf(obj modelfacts.APIObject) (map[string]any, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var whole struct {
		Spec map[string]any `json:"spec"`
	}
	if err := json.Unmarshal(data, &whole); err != nil {
		return nil, err
	}
	return whole.Spec, nil
}

// mappingValue is the mapping under key in m, added when m has none.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key && m.Content[i+1].Kind == yaml.MappingNode {
			return m.Content[i+1]
		}
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: key}, v)
	return v
}

// setKey sets key in mapping m to v, in place when m has it, removing it
// when v is nil.
func setKey(m *yaml.Node, key string, v any) error {
	i := 0
	for ; i+1 < len(m.Content) && m.Content[i].Value != key; i += 2 {
	}
	if v == nil {
		if i+1 < len(m.Content) {
			m.Content = slices.Delete(m.Content, i, i+2)
		}
		return nil
	}
	var value yaml.Node
	if err := value.Encode(v); err != nil {
		return err
	}
	if i+1 < len(m.Content) {
		value.HeadComment, value.LineComment = m.Content[i+1].HeadComment, m.Content[i+1].LineComment
		m.Content[i+1] = &value
		return nil
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: key}, &value)
	return nil
}

// newDocument is a created object as a model file's document: its
// apiVersion, kind, name and the spec fields it sets, in the API type's
// order.
func newDocument(ns string, o model.PlanObject) ([]byte, error) {
	obj, err := modelfacts.StoryObject(ns, o)
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return nil, err
	}
	var whole struct {
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(data, &whole); err != nil {
		return nil, err
	}
	// JSON is YAML: read as a node, the spec keeps the type's field order.
	var spec yaml.Node
	if err := yaml.Unmarshal(whole.Spec, &spec); err != nil {
		return nil, err
	}
	str := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagStr, Value: v} }
	doc := &yaml.Node{Kind: yaml.MappingNode, Tag: tagMap, Content: []*yaml.Node{
		str("apiVersion"), str(c4v1beta1.GroupVersion.String()), str("kind"), str(o.Kind),
		str("metadata"), {Kind: yaml.MappingNode, Tag: tagMap, Content: []*yaml.Node{str("name"), str(o.Name)}},
	}}
	if len(spec.Content) > 0 && prune(spec.Content[0]) {
		doc.Content = append(doc.Content, str("spec"), spec.Content[0])
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// prune drops from n what a plan's object leaves unset (empty strings,
// false, nulls, and lists and maps left empty by that) and writes it in
// block style, and reports whether anything is left. A number stays, zero
// too: a zero retention is a choice.
func prune(n *yaml.Node) bool {
	n.Style = 0
	switch n.Kind {
	case yaml.MappingNode:
		kept := n.Content[:0]
		for i := 0; i+1 < len(n.Content); i += 2 {
			if prune(n.Content[i+1]) {
				n.Content[i].Style = 0
				kept = append(kept, n.Content[i], n.Content[i+1])
			}
		}
		n.Content = kept
		return len(kept) > 0
	case yaml.SequenceNode:
		kept := n.Content[:0]
		for _, e := range n.Content {
			if prune(e) {
				kept = append(kept, e)
			}
		}
		n.Content = kept
		return len(kept) > 0
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!null":
			return false
		case tagStr:
			return n.Value != ""
		case "!!bool":
			return n.Value != "false"
		}
	}
	return true
}

// modelObject is m's object of ref, as Decode read it.
func modelObject(m *Model, ref model.Ref) (modelfacts.APIObject, error) {
	var obj modelfacts.APIObject
	switch ref.Kind {
	case model.KindProject:
		if p := m.Objects.Project; p != nil && p.Name == ref.Name {
			obj = p
		}
	case model.KindPerson:
		obj = found(modelfacts.Find(m.People, ref.Name))
	case model.KindSoftwareSystem:
		obj = found(modelfacts.Find(m.Systems, ref.Name))
	case model.KindContainer:
		obj = found(modelfacts.Find(m.Containers, ref.Name))
	case model.KindDataStore:
		obj = found(modelfacts.Find(m.Objects.DataStores, ref.Name))
	case model.KindRelationship:
		obj = found(modelfacts.Find(m.Objects.Relationships, ref.Name))
	}
	if obj == nil {
		return nil, fmt.Errorf("%s holds no %s", ModelFile, ref)
	}
	return obj, nil
}

// found is p as an APIObject, nil when p is nil.
func found[P interface {
	*T
	modelfacts.APIObject
}, T any](p P) modelfacts.APIObject {
	if p == nil {
		return nil
	}
	return p
}
