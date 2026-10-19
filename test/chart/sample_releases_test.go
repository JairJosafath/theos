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
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// sampleTag is a tag a Crew that tags releases pushes: v, then the version.
const sampleTag = "v0.1.0"

// sampleObject is the part of a sample's object this test reads.
type sampleObject struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		// A Container's.
		Type          string     `yaml:"type"`
		FrameworkRef  *sampleRef `yaml:"frameworkRef"`
		StandardRef   *sampleRef `yaml:"standardRef"`
		ComputeRef    *sampleRef `yaml:"computeRef"`
		DeploymentRef *sampleRef `yaml:"deploymentRef"`
		Checks        string     `yaml:"checks"`
		// A Crew's.
		ContainerRef *sampleRef `yaml:"containerRef"`
		TagReleases  bool       `yaml:"tagReleases"`
	} `yaml:"spec"`
}

type sampleRef struct {
	Name string `yaml:"name"`
}

// TestSampleCrewsThatTagReleasesGetAReleaseRun holds every Crew of
// config/samples that tags releases to a Container whose repository, rendered
// from templates/ as the operator scaffolds it, has a Gitea workflow that runs
// on the tag (#1215). Without one a tag starts no run, the release's run stays
// Waiting, and the Crew never publishes it: first-app's crew-publish.yaml on an
// orders-cli with no gitea-release did that. The Container is scaffolded once,
// so the sample has to name the Deployment before the guide applies it.
func TestSampleCrewsThatTagReleasesGetAReleaseRun(t *testing.T) {
	root := filepath.Join("..", "..")
	// Each sample folder is applied on its own, so a Crew's Container is the
	// one of its folder.
	containers := map[string]map[string]sampleObject{}
	type crew struct{ dir, file, name, container string }
	var crews []crew
	samples := filepath.Join(root, "config", "samples")
	err := filepath.WalkDir(samples, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || (!strings.HasSuffix(p, ".yaml") && !strings.HasSuffix(p, ".yml")) {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dir := filepath.Dir(p)
		file, _ := filepath.Rel(root, p)
		dec := yaml.NewDecoder(bytes.NewReader(data))
		for {
			var o sampleObject
			if err := dec.Decode(&o); errors.Is(err, io.EOF) {
				return nil
			} else if err != nil {
				return &fs.PathError{Op: "decode", Path: p, Err: err}
			}
			switch {
			case o.Kind == "Container":
				if containers[dir] == nil {
					containers[dir] = map[string]sampleObject{}
				}
				containers[dir][o.Metadata.Name] = o
			case o.Kind == "Crew" && o.Spec.TagReleases && o.Spec.ContainerRef != nil:
				crews = append(crews, crew{dir, file, o.Metadata.Name, o.Spec.ContainerRef.Name})
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(crews) == 0 {
		t.Fatal("no sample Crew tags releases; config/samples/first-app/crew-publish.yaml did")
	}

	ci, err := model.CIByName(model.CIGiteaActions)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range crews {
		t.Run(c.file, func(t *testing.T) {
			ctr, ok := containers[c.dir][c.container]
			if !ok {
				t.Fatalf("Crew %s tags releases of Container %s, which no sample in %s makes", c.name, c.container, c.dir)
			}
			files := renderSample(t, root, ctr, ci)
			var runs []string
			for _, p := range files.Paths() {
				if dir, _ := path.Split(p); dir != ".gitea/workflows/" {
					continue
				}
				if workflowRunsOnTag(t, p, files[p].Data, sampleTag) {
					runs = append(runs, p)
				}
			}
			if len(runs) == 0 {
				t.Errorf("Crew %s (%s) tags releases of Container %s, but no workflow of its repository runs on tag %s, "+
					"so the release's run stays Waiting and is never published: name a Deployment that ships releases, "+
					"such as deploymentRef: {name: gitea-release}, in the Container (#1215)",
					c.name, c.file, c.container, sampleTag)
			}
		})
	}
}

// renderSample renders a sample Container's repository from templates/, as
// the operator scaffolds it with the factory's default operations.
func renderSample(t *testing.T, root string, ctr sampleObject, ci model.CI) scaffold.Tree {
	t.Helper()
	in := scaffold.Input{
		Project: "webshop", SoftwareSystem: "shop",
		Container: ctr.Metadata.Name, DisplayName: ctr.Metadata.Name, Type: ctr.Spec.Type,
		Checks: ctr.Spec.Checks, CI: ci,
		Operations: model.ResolveOperations(model.OperationsChoices{}),
	}
	var layers []scaffold.Layer
	add := func(side string, ref *sampleRef, name *string) {
		if ref == nil {
			return
		}
		files, err := scaffold.ReadTree(os.DirFS(filepath.Join(root, "templates", ref.Name)))
		if err != nil {
			t.Fatalf("Container %s's %s %s: %v", ctr.Metadata.Name, side, ref.Name, err)
		}
		layers = append(layers, scaffold.Layer{Side: side, Name: ref.Name, Ref: "local", Files: files})
		*name = ref.Name
	}
	add("Framework", ctr.Spec.FrameworkRef, &in.Framework)
	add("Standard", ctr.Spec.StandardRef, &in.Standard)
	add("Compute", ctr.Spec.ComputeRef, &in.Compute)
	add("Deployment", ctr.Spec.DeploymentRef, &in.Deployment)
	result, err := scaffold.Build(in, layers, scaffold.Options{})
	if err != nil {
		t.Fatalf("render Container %s: %v", ctr.Metadata.Name, err)
	}
	return result.Files
}

// workflowRunsOnTag reports whether the Gitea workflow at p runs when tag is
// pushed: one of its on.push.tags patterns matches it.
func workflowRunsOnTag(t *testing.T, p string, data []byte, tag string) bool {
	t.Helper()
	var w struct {
		On struct {
			Push struct {
				Tags []string `yaml:"tags"`
			} `yaml:"push"`
		} `yaml:"on"`
	}
	if err := yaml.Unmarshal(data, &w); err != nil {
		// A workflow whose on is a word or a list has no tag filter, so it
		// doesn't run on a tag the way release.yaml does.
		var probe map[string]any
		if yaml.Unmarshal(data, &probe) != nil {
			t.Fatalf("%s: %v", p, err)
		}
		return false
	}
	for _, pattern := range w.On.Push.Tags {
		if ok, err := path.Match(pattern, tag); err != nil {
			t.Fatalf("%s: tag pattern %q: %v", p, pattern, err)
		} else if ok {
			return true
		}
	}
	return false
}
