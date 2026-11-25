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

package modelfacts_test

import (
	"reflect"
	"testing"

	"k8s.io/apimachinery/pkg/api/equality"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/modelfacts"
)

func TestStoryObjectWritesOnlyThePlannedOperations(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   *model.OperationsChoices
		want *c4v1beta1.OperationsSpec
	}{
		{"left out", nil, nil},
		{"one field", &model.OperationsChoices{RPO: "15m"},
			&c4v1beta1.OperationsSpec{Recovery: &c4v1beta1.OperationsRecovery{RPO: "15m"}}},
		{"a zero retention is a choice", &model.OperationsChoices{RetentionDays: new(int32(0)), Email: "a@b.io"},
			&c4v1beta1.OperationsSpec{Backups: &c4v1beta1.OperationsBackups{RetentionDays: new(int32(0))},
				Alerts: &c4v1beta1.OperationsAlerts{Email: "a@b.io"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			obj, err := modelfacts.StoryObject("shop", model.PlanObject{Kind: model.KindContainer, Name: "api",
				Operations: tt.in})
			if err != nil {
				t.Fatal(err)
			}
			got := obj.(*c4v1beta1.Container).Spec.Operations
			if !equality.Semantic.DeepEqual(got, tt.want) {
				t.Errorf("spec.operations = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// #293: a plan's object of each kind, made into the API's and read back as
// a plan's, is the object it was: theos plan and the Brief round write and
// read the story through these two functions.
func TestStoryObjectsReadBackStoryObject293(t *testing.T) {
	objects := []model.PlanObject{
		{Kind: model.KindProject, Name: "shop", DisplayName: "Shop", Description: "A shop."},
		{Kind: model.KindPerson, Name: "buyer", DisplayName: "Buyer"},
		{Kind: model.KindSoftwareSystem, Name: "core", Description: "The shop.", Needs: []string{"buy"},
			KPIs: []string{"orders a day"}, Location: model.LocationInternal, Domains: []string{"commerce"}},
		{Kind: model.KindContainer, Name: "web", SoftwareSystem: "core", Type: model.TypeStaticSite,
			GitProvider: "forge", Framework: "astro", Standard: "ts", Checks: model.ChecksBeforeRelease,
			Operations: &model.OperationsChoices{RPO: "15m"}},
		{Kind: model.KindContainer, Name: "api", SoftwareSystem: "core", Type: model.TypeService,
			GitProvider: "forge", Bundle: "go-service"},
		{Kind: model.KindDataStore, Name: "db", SoftwareSystem: "core", Connector: "postgres"},
		{Kind: model.KindRelationship, Name: "web-to-api", Description: "calls",
			Source: model.Ref{Kind: model.KindContainer, Name: "web"},
			Target: model.Ref{Kind: model.KindContainer, Name: "api"}, Bundles: true},
	}
	var story modelfacts.Story
	for _, o := range objects {
		obj, err := modelfacts.StoryObject("shop", o)
		if err != nil {
			t.Fatal(err)
		}
		if obj.GetNamespace() != "shop" || obj.GetName() != o.Name {
			t.Errorf("%s is %s/%s", o.Ref(), obj.GetNamespace(), obj.GetName())
		}
		switch v := obj.(type) {
		case *c4v1beta1.Project:
			story.Projects = append(story.Projects, *v)
		case *c4v1beta1.Person:
			story.People = append(story.People, *v)
		case *c4v1beta1.SoftwareSystem:
			story.Systems = append(story.Systems, *v)
		case *c4v1beta1.Container:
			story.Containers = append(story.Containers, *v)
		case *c4v1beta1.DataStore:
			story.DataStores = append(story.DataStores, *v)
		case *c4v1beta1.Relationship:
			story.Relationships = append(story.Relationships, *v)
		}
	}
	if got := modelfacts.StoryObjects(story); !reflect.DeepEqual(got, objects) {
		t.Errorf("read back as\n%+v\nwant\n%+v", got, objects)
	}
	if _, err := modelfacts.StoryObject("shop", model.PlanObject{Kind: "Crew", Name: "web"}); err == nil {
		t.Error("a Crew was made from a plan's object")
	}
}

// #293: CopyStoryText changes only the story text and a set checks; a
// Container's adopt, license, checkCommand, deploy and operations, and a
// system's location and Domains, stay as they are.
func TestCopyStoryTextKeepsWhatAPlanCantWrite293(t *testing.T) {
	ops := &c4v1beta1.OperationsSpec{Alerts: &c4v1beta1.OperationsAlerts{Email: "ops@shop.io"}}
	existing := &c4v1beta1.Container{Spec: c4v1beta1.ContainerSpec{DisplayName: "Web", Description: "old",
		Adopt: true, License: "MIT", CheckCommand: "make check", Checks: c4v1beta1.ChecksMode(model.ChecksEveryChange),
		Deploy: []c4v1beta1.ContainerDeploy{{Target: "prod"}}, Operations: ops}}
	want := existing.DeepCopy()
	want.Spec.Description, want.Spec.Checks = "new", c4v1beta1.ChecksMode(model.ChecksBeforeRelease)
	plan, err := modelfacts.StoryObject("shop", model.PlanObject{Kind: model.KindContainer, Name: "web",
		DisplayName: "Web", Description: "new", Checks: model.ChecksBeforeRelease})
	if err != nil {
		t.Fatal(err)
	}
	modelfacts.CopyStoryText(existing, plan)
	if !equality.Semantic.DeepEqual(existing, want) {
		t.Errorf("copied to\n%+v\nwant\n%+v", existing.Spec, want.Spec)
	}

	// A plan that leaves checks out leaves them alone.
	plan.(*c4v1beta1.Container).Spec.Checks = ""
	modelfacts.CopyStoryText(existing, plan)
	if existing.Spec.Checks != c4v1beta1.ChecksMode(model.ChecksBeforeRelease) {
		t.Errorf("checks are %q", existing.Spec.Checks)
	}

	system := &c4v1beta1.SoftwareSystem{Spec: c4v1beta1.SoftwareSystemSpec{Location: "External",
		DomainRefs: []string{"commerce"}}}
	src, err := modelfacts.StoryObject("shop", model.PlanObject{Kind: model.KindSoftwareSystem, Name: "core",
		Needs: []string{"buy"}, Location: model.LocationInternal})
	if err != nil {
		t.Fatal(err)
	}
	modelfacts.CopyStoryText(system, src)
	if system.Spec.Location != "External" || len(system.Spec.DomainRefs) != 1 || len(system.Spec.Needs) != 1 {
		t.Errorf("the system is %+v", system.Spec)
	}
}
