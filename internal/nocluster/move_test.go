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

package nocluster_test

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"sigs.k8s.io/yaml"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/nocluster"
)

var updateSample = flag.Bool("update", false, "write config/samples/no-cluster-export/webshop.yaml again")

// exportSample is the exported project make validate-yaml checks
// (#295).
var exportSample = filepath.Join("..", "..", "config", "samples", "no-cluster-export", "webshop.yaml")

// exportSampleHeader opens the sample, before the stream Export writes.
const exportSampleHeader = `# What theos export prints for the webshop worked with no cluster
# (docs/no-cluster.md, "Move to a cluster and back"): the samples' webshop
# story, catalog and Crew, every Container scaffolded but payments-worker.
# TestExportSample295 writes it (go test ./internal/nocluster -run
# TestExportSample295 -update) and make validate-yaml checks it.
`

// exportWorld is the webshop with its Crew, scaffolded on the fake host
// but for payments-worker, and its model files.
func exportWorld(t *testing.T) (*world, fstest.MapFS) {
	t.Helper()
	w := newWorld(t)
	shop := readWebshop(t)
	crew, err := os.ReadFile(filepath.Join("..", "..", "config", "samples", "webshop", "crew.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	shop.setup += "\n---\n" + string(crew)
	w.m = decode(t, shop, "")
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	w.git.ClearRepo(w.provider().Spec.Owner, "payments-worker")
	return w, fstest.MapFS{nocluster.ModelFile: {Data: []byte(shop.story)},
		nocluster.SetupFile: {Data: []byte(shop.setup)}}
}

// streamObjects are the objects of a YAML stream, by kind and name.
func streamObjects(t *testing.T, data []byte) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	for _, doc := range documents(t, data, false) {
		meta, _ := doc["metadata"].(map[string]any)
		name, _ := meta["name"].(string)
		out[doc["kind"].(string)+"/"+name] = doc
	}
	return out
}

// #295: theos export puts adopt: true on every Container whose repository
// has commits, and on no other; every object in the Project's namespace;
// every Crew holding every change for a person, and one added for each
// scaffolded Container no Crew works; the documents' comments kept.
func TestExportAdopts295(t *testing.T) {
	w, fsys := exportWorld(t)
	var stream bytes.Buffer
	if err := nocluster.Export(context.Background(), w.env, fsys, &stream); err != nil {
		t.Fatal(err)
	}
	objs := streamObjects(t, stream.Bytes())
	for key, obj := range objs {
		if ns := obj["metadata"].(map[string]any)["namespace"]; ns != "webshop" {
			t.Errorf("#295 %s in namespace %v", key, ns)
		}
	}
	for _, c := range w.m.Containers {
		spec, _ := objs["Container/"+c.Name]["spec"].(map[string]any)
		adopt, _ := spec["adopt"].(bool)
		if want := c.Name != "payments-worker"; adopt != want {
			t.Errorf("#295 Container %s: adopt %v, want %v", c.Name, adopt, want)
		}
	}
	personOnly := []any{map[string]any{"require": "Person"}}
	for _, crew := range []string{"orders-api", "catalog-api", "storefront"} {
		spec, _ := objs["Crew/"+crew]["spec"].(map[string]any)
		ref, _ := spec["containerRef"].(map[string]any)
		if !reflect.DeepEqual(spec["approval"], personOnly) || ref["name"] != crew {
			t.Errorf("#295 Crew %s: %v", crew, spec)
		}
	}
	if _, ok := objs["Crew/payments-worker"]; ok {
		t.Error("#295 a Crew was added for payments-worker, which has no repository")
	}
	if spec := objs["Crew/orders-api"]["spec"].(map[string]any); spec["parallel"] != float64(2) {
		t.Errorf("#295 the Crew lost its fields: %v", spec)
	}
	if !strings.Contains(stream.String(), "# The webshop Domain (factory.yaml)") {
		t.Error("#295 the story's comments are not in the stream")
	}
	for _, note := range []string{"Container orders-api: adopt: true", "Crew catalog-api: added for Container catalog-api"} {
		if !strings.Contains(w.out.String(), note) {
			t.Errorf("#295 export does not say %q:\n%s", note, w.out)
		}
	}
	if strings.Contains(stream.String(), w.token) {
		t.Error("#295 the stream holds the token")
	}
}

// #295: the exported project under config/samples/ is what theos
// export prints, so make validate-yaml checks the real thing.
func TestExportSample295(t *testing.T) {
	w, fsys := exportWorld(t)
	var stream bytes.Buffer
	stream.WriteString(exportSampleHeader)
	if err := nocluster.Export(context.Background(), w.env, fsys, &stream); err != nil {
		t.Fatal(err)
	}
	if *updateSample {
		if err := os.MkdirAll(filepath.Dir(exportSample), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exportSample, stream.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(exportSample)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stream.Bytes(), want) {
		t.Errorf("#295 %s is not what theos export prints; run this test with -update", exportSample)
	}
}

// asCluster is objs as kubectl get -o yaml prints them: one List, each item
// with what a cluster adds (a uid, a resourceVersion, managed fields,
// kubectl's annotation, an owner, a status), and a Task and a Secret beside
// them.
func asCluster(t *testing.T, objs map[string]map[string]any) []byte {
	t.Helper()
	items := make([]any, 0, len(objs)+2)
	for _, key := range slicesSorted(objs) {
		obj := objs[key]
		meta, _ := obj["metadata"].(map[string]any)
		meta["uid"], meta["resourceVersion"], meta["generation"] = "0b1c", "4711", 3
		meta["creationTimestamp"] = "2026-11-20T10:00:00Z"
		meta["managedFields"] = []any{map[string]any{"manager": "kubectl", "operation": "Apply"}}
		meta["annotations"] = map[string]any{"kubectl.kubernetes.io/last-applied-configuration": "{}",
			"theos.factory.io/retry": "1", "team": "shop"}
		meta["ownerReferences"] = []any{map[string]any{"kind": "Project", "name": "webshop"}}
		obj["status"] = map[string]any{"phase": "Ready"}
		if strings.HasPrefix(key, "Crew/") {
			obj["spec"].(map[string]any)["paused"] = true
		}
		items = append(items, obj)
	}
	items = append(items,
		map[string]any{"apiVersion": "theos.factory.io/v1beta1", "kind": "Task",
			"metadata": map[string]any{"name": "orders-api-7", "namespace": "webshop"}},
		map[string]any{"apiVersion": "v1", "kind": "Secret",
			"metadata": map[string]any{"name": "host-credentials", "namespace": "webshop"}})
	data, err := yaml.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": items})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func slicesSorted(m map[string]map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// #295: theos import reads a project's objects as kubectl get -o yaml
// prints them and writes model files that decode to the same model: what
// a cluster adds is dropped, a Crew's pause too, other kinds are named and
// left out, and an exported model comes back with its Containers' adopt.
func TestImportReadsAClustersExport295(t *testing.T) {
	w, fsys := exportWorld(t)
	var stream bytes.Buffer
	if err := nocluster.Export(context.Background(), w.env, fsys, &stream); err != nil {
		t.Fatal(err)
	}
	imported, err := nocluster.Import(asCluster(t, streamObjects(t, stream.Bytes())), nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := nocluster.Decode(imported.Story, imported.Setup, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Containers) != len(w.m.Containers) || len(m.Objects.Relationships) != len(w.m.Objects.Relationships) ||
		len(m.Objects.Standards) != len(w.m.Objects.Standards) || len(m.Crews) != 3 || len(m.Agents) != 2 {
		t.Fatalf("#295 imported %d Containers, %d Relationships, %d Standards, %d Crews, %d Agents", len(m.Containers),
			len(m.Objects.Relationships), len(m.Objects.Standards), len(m.Crews), len(m.Agents))
	}
	for _, c := range w.m.Containers {
		got := modelfacts.Find(m.Containers, c.Name)
		want := c.Spec.DeepCopy()
		want.Adopt = c.Name != "payments-worker"
		if got == nil || !reflect.DeepEqual(&got.Spec, want) {
			t.Errorf("#295 Container %s imported as %+v", c.Name, got)
		}
		if got != nil && (got.Annotations["team"] != "shop" || len(got.Annotations) != 1 || got.UID != "" ||
			len(got.OwnerReferences) != 0) {
			t.Errorf("#295 Container %s kept the cluster's metadata: %+v", c.Name, got.ObjectMeta)
		}
	}
	for _, c := range m.Crews {
		if c.Spec.Paused {
			t.Errorf("#295 Crew %s is still paused", c.Name)
		}
	}
	notes := strings.Join(imported.Notes, "\n")
	for _, want := range []string{"left out 1 secret", "left out 1 task", "Crew orders-api: paused dropped"} {
		if !strings.Contains(notes, want) {
			t.Errorf("#295 import does not say %q:\n%s", want, notes)
		}
	}
	for _, not := range []string{"status:", "resourceVersion", "managedFields", "last-applied", "namespace:"} {
		if bytes.Contains(imported.Story, []byte(not)) || bytes.Contains(imported.Setup, []byte(not)) {
			t.Errorf("#295 the imported files hold %q", not)
		}
	}
}

// #295: import refuses an export of two namespaces, or one with no
// Project, and one whose model does not decode.
func TestImportRefuses295(t *testing.T) {
	project := func(ns string) map[string]any {
		return map[string]any{"apiVersion": "theos.factory.io/v1beta1", "kind": "Project",
			"metadata": map[string]any{"name": ns, "namespace": ns}}
	}
	list := func(items ...any) []byte {
		data, err := yaml.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": items})
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	system := map[string]any{"apiVersion": "theos.factory.io/v1beta1", "kind": "SoftwareSystem",
		"metadata": map[string]any{"name": "core", "namespace": "shop"}, "spec": map[string]any{"displayName": "Core"}}
	for name, data := range map[string][]byte{
		"two namespaces": list(project("shop"), project("other")),
		"no Project":     list(system),
		"an unknown field": list(project("shop"), map[string]any{"apiVersion": "theos.factory.io/v1beta1",
			"kind": "SoftwareSystem", "metadata": map[string]any{"name": "core", "namespace": "shop"},
			"spec": map[string]any{"displayName": "Core", "colour": "red"}}),
	} {
		if _, err := nocluster.Import(data, nil); err == nil {
			t.Errorf("#295 %s: imported", name)
		}
	}
	if got, err := nocluster.Import(list(project("shop"), system), nil); err != nil ||
		!bytes.Contains(got.Story, []byte("kind: SoftwareSystem")) {
		t.Errorf("#295 a plain project: %v", err)
	}
}

// #295: a Crew holding every change for a person: the stream's Crews read
// as the API's types do, specApproval Person for one with a spec writer.
func TestExportCrewsHoldForAPerson295(t *testing.T) {
	w, fsys := exportWorld(t)
	setup := string(fsys[nocluster.SetupFile].Data)
	setup = strings.Replace(setup, "  parallel: 2\n", "  parallel: 2\n  specWriter:\n    name: implementer\n"+
		"  specApproval: Reviewer\n  approval:\n  - require: None\n", 1)
	fsys[nocluster.SetupFile] = &fstest.MapFile{Data: []byte(setup)}
	var stream bytes.Buffer
	if err := nocluster.Export(context.Background(), w.env, fsys, &stream); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(streamObjects(t, stream.Bytes())["Crew/orders-api"])
	if err != nil {
		t.Fatal(err)
	}
	var crew c4v1beta1.Crew
	if err := yaml.UnmarshalStrict(data, &crew); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(crew.Spec.Approval, []c4v1beta1.ApprovalRule{{Require: c4v1beta1.ApprovalPerson}}) ||
		crew.Spec.SpecApproval != c4v1beta1.SpecApprovalPerson {
		t.Errorf("#295 the Crew holds %+v, spec approval %q", crew.Spec.Approval, crew.Spec.SpecApproval)
	}
}
