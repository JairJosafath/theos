/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package chart

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/c4factory/internal/model"
)

// webshopGaps are the webshop's Containers whose catalog choices the sample's
// catalog can't take yet, each with the issue that closes the gap. Once it
// lands, the Container's choices fit and the test says to remove its line.
var webshopGaps = map[string]string{
	"payments-worker": "#1468: no Framework runs a worker on Kubernetes",
}

// TestWebshopSampleIsTheReferenceApp holds config/samples/webshop to the
// reference application of docs/plans/fresh-start.md (#1428): four Containers
// and a DataStore, each Container's choices in the sample's catalog, and a
// Brief whose stand-in plan lists them all and changes none of them.
func TestWebshopSampleIsTheReferenceApp(t *testing.T) {
	dir := filepath.Join("..", "..", "config", "samples", "webshop")
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	story, err := model.ParsePlan(map[string]string{model.PlanModelFile: string(read("story.yaml"))})
	if err != nil {
		t.Fatalf("story.yaml: %v", err)
	}
	objects := story.Objects
	for i := range objects {
		objects[i].APIVersion = "" // the namespace's objects, not a plan's
	}
	catalog := webshopCatalog(t, read("factory.yaml"))
	var brief struct {
		Metadata struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Idea string `yaml:"idea"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(read("brief.yaml"), &brief); err != nil || brief.Spec.Idea == "" {
		t.Fatalf("brief.yaml: want a Brief with an idea, got %+v (%v)", brief, err)
	}

	wantContainers := []string{"catalog-api", "orders-api", "payments-worker", "storefront"}
	wantDataStores := []string{"shop-db"}
	if c, d := webshopNames(objects, model.KindContainer), webshopNames(objects, model.KindDataStore); !slices.Equal(c, wantContainers) ||
		!slices.Equal(d, wantDataStores) {
		t.Fatalf("story.yaml has Containers %v and DataStores %v, want %v and %v", c, d, wantContainers, wantDataStores)
	}

	for _, o := range objects {
		if o.Kind != model.KindContainer {
			continue
		}
		err := webshopChoices(o, objects, catalog)
		gap, isGap := webshopGaps[o.Name]
		switch {
		case isGap && err == nil:
			t.Errorf("Container %s's choices fit now: remove its line from webshopGaps (%s)", o.Name, gap)
		case !isGap && err != nil:
			t.Errorf("Container %s: %v", o.Name, err)
		}
	}

	ws := model.BriefWorkspace{Brief: brief.Metadata.Name, Idea: brief.Spec.Idea, Namespace: "webshop",
		GitProvider: "gitea", Objects: objects, Catalog: catalog}
	workspace, err := model.BriefWorkspaceFiles(ws)
	if err != nil {
		t.Fatal(err)
	}
	files, err := model.StandInPlan(workspace)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := model.ParsePlan(files)
	if err != nil {
		t.Fatalf("ParsePlan: %v\n%v", err, files)
	}
	current := model.CurrentModel{Namespace: ws.Namespace, Objects: objects, Catalog: catalog, GitProvider: ws.GitProvider}
	if refusals := model.CheckPlan(plan, current); len(refusals) > 0 {
		t.Fatalf("CheckPlan refuses the stand-in's plan: %v\n%s", refusals, files[model.PlanModelFile])
	}
	if _, changed := model.PlanDiff(plan, current); len(changed) > 0 {
		t.Errorf("the plan changes %v, want the model kept", changed)
	}
	containers := webshopNames(plan.Objects, model.KindContainer)
	for _, c := range wantContainers {
		if !slices.Contains(containers, c) {
			t.Errorf("the stand-in's plan lists Containers %v, want %s among them", containers, c)
		}
	}
	if d := webshopNames(plan.Objects, model.KindDataStore); !slices.Equal(d, wantDataStores) {
		t.Errorf("the stand-in's plan lists DataStores %v, want %v", d, wantDataStores)
	}
}

// webshopCatalog is the catalog entries of the sample's factory.yaml.
func webshopCatalog(t *testing.T, data []byte) []model.CatalogEntry {
	t.Helper()
	var entries []model.CatalogEntry
	dec := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var o struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				SupportedTypes      []string `yaml:"supportedTypes"`
				SupportedFrameworks []string `yaml:"supportedFrameworks"`
				DeploysItself       bool     `yaml:"deploysItself"`
			} `yaml:"spec"`
		}
		if err := dec.Decode(&o); errors.Is(err, io.EOF) {
			return entries
		} else if err != nil {
			t.Fatalf("factory.yaml: %v", err)
		}
		switch o.Kind {
		case model.SideFramework, model.SideStandard, model.SideCompute, model.SideDeployment, model.SideConnector:
			entries = append(entries, model.CatalogEntry{Side: o.Kind, Name: o.Metadata.Name,
				SupportedTypes: o.Spec.SupportedTypes, SupportedFrameworks: o.Spec.SupportedFrameworks,
				DeploysItself: o.Spec.DeploysItself})
		}
	}
}

// webshopChoices checks Container c's choices as the operator does: each in
// the catalog and supporting c's type, the Connectors of the DataStores it
// links to included, and its Standard fitting its Framework.
func webshopChoices(c model.PlanObject, objects []model.PlanObject, catalog []model.CatalogEntry) error {
	find := func(side, name string) (model.CatalogEntry, error) {
		i := slices.IndexFunc(catalog, func(e model.CatalogEntry) bool { return e.Side == side && e.Name == name })
		if i < 0 {
			return model.CatalogEntry{}, errors.New("the catalog has no " + side + " " + name)
		}
		return catalog[i], nil
	}
	refs := [][2]string{{model.SideFramework, c.Framework}, {model.SideStandard, c.Standard},
		{model.SideCompute, c.Compute}, {model.SideDeployment, c.Deployment}}
	for _, r := range objects {
		if r.Kind == model.KindRelationship && r.Source == (model.Ref{Kind: model.KindContainer, Name: c.Name}) &&
			r.Target.Kind == model.KindDataStore {
			for _, d := range objects {
				if d.Kind == model.KindDataStore && d.Name == r.Target.Name {
					refs = append(refs, [2]string{model.SideConnector, d.Connector})
				}
			}
		}
	}
	var entries []model.CatalogEntry
	var standard *model.CatalogEntry
	for _, r := range refs {
		if r[1] == "" {
			continue
		}
		e, err := find(r[0], r[1])
		if err != nil {
			return err
		}
		if r[0] == model.SideStandard {
			standard = &e
		}
		entries = append(entries, e)
	}
	if err := model.CheckChoices(c.Type, entries); err != nil {
		return err
	}
	return model.CheckStandard(c.Framework, standard)
}

// webshopNames is the sorted names of objects of kind.
func webshopNames(objects []model.PlanObject, kind string) []string {
	var names []string
	for _, o := range objects {
		if o.Kind == kind {
			names = append(names, o.Name)
		}
	}
	slices.Sort(names)
	return names
}
