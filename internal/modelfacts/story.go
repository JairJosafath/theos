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

package modelfacts

import (
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// A plan's objects there and back (docs/plans/no-cluster.md, decision 4;
// #293): the Brief reconciler and theos plan read a project's story objects
// as model.PlanObjects with StoryObjects, and turn a plan's object into the
// API's with StoryObject and CopyStoryText, so a plan applied without a
// cluster changes what the operator's round would.

// APIObject is one of the API's objects: the method set of
// controller-runtime's client.Object, which this package does not import.
type APIObject interface {
	metav1.Object
	runtime.Object
}

// Story is a project's story objects, the kinds a plan writes, as the
// caller read them.
type Story struct {
	Projects      []c4v1beta1.Project
	People        []c4v1beta1.Person
	Systems       []c4v1beta1.SoftwareSystem
	Containers    []c4v1beta1.Container
	DataStores    []c4v1beta1.DataStore
	Relationships []c4v1beta1.Relationship
}

// StoryObjects are the story's objects as a plan's objects, kind by kind in
// the order the story lists them.
func StoryObjects(s Story) []model.PlanObject {
	out := make([]model.PlanObject, 0, len(s.Projects)+len(s.People)+len(s.Systems)+len(s.Containers)+
		len(s.DataStores)+len(s.Relationships))
	for _, o := range s.Projects {
		out = append(out, model.PlanObject{Kind: model.KindProject, Name: o.Name,
			DisplayName: o.Spec.DisplayName, Description: o.Spec.Description})
	}
	for _, o := range s.People {
		out = append(out, model.PlanObject{Kind: model.KindPerson, Name: o.Name,
			DisplayName: o.Spec.DisplayName, Description: o.Spec.Description})
	}
	for _, o := range s.Systems {
		out = append(out, model.PlanObject{Kind: model.KindSoftwareSystem, Name: o.Name,
			DisplayName: o.Spec.DisplayName, Description: o.Spec.Description, Needs: o.Spec.Needs, KPIs: o.Spec.KPIs,
			Location: string(o.Spec.Location), Domains: o.Spec.DomainRefs})
	}
	for i := range s.Containers {
		o := &s.Containers[i]
		spec, picked := o.Spec, o.Choices()
		out = append(out, model.PlanObject{Kind: model.KindContainer, Name: o.Name,
			DisplayName: spec.DisplayName, Description: spec.Description, SoftwareSystem: spec.SoftwareSystemRef.Name,
			Type: string(spec.Type), GitProvider: spec.GitProviderRef.Name, Bundle: picked.Bundle,
			Framework: picked.Framework, Standard: picked.Standard, Compute: picked.Compute, Deployment: picked.Deployment,
			Checks: string(spec.Checks), Operations: PlannedOperations(spec.Operations)})
	}
	for _, o := range s.DataStores {
		out = append(out, model.PlanObject{Kind: model.KindDataStore, Name: o.Name,
			DisplayName: o.Spec.DisplayName, Description: o.Spec.Description,
			SoftwareSystem: o.Spec.SoftwareSystemRef.Name, Connector: o.Spec.ConnectorRef.Name})
	}
	for _, o := range s.Relationships {
		spec := o.Spec
		out = append(out, model.PlanObject{Kind: model.KindRelationship, Name: o.Name, Description: spec.Description,
			Source:  model.Ref{Kind: string(spec.Source.Kind), Name: spec.Source.Name},
			Target:  model.Ref{Kind: string(spec.Target.Kind), Name: spec.Target.Name},
			Bundles: spec.Bundles})
	}
	return out
}

// StoryObject is a plan's object as the kind it is, in namespace ns.
func StoryObject(ns string, o model.PlanObject) (APIObject, error) {
	objMeta := metav1.ObjectMeta{Namespace: ns, Name: o.Name}
	switch o.Kind {
	case model.KindProject:
		return &c4v1beta1.Project{ObjectMeta: objMeta,
			Spec: c4v1beta1.ProjectSpec{DisplayName: o.DisplayName, Description: o.Description}}, nil
	case model.KindPerson:
		return &c4v1beta1.Person{ObjectMeta: objMeta,
			Spec: c4v1beta1.PersonSpec{DisplayName: o.DisplayName, Description: o.Description}}, nil
	case model.KindSoftwareSystem:
		return &c4v1beta1.SoftwareSystem{ObjectMeta: objMeta, Spec: c4v1beta1.SoftwareSystemSpec{
			DisplayName: o.DisplayName, Description: o.Description, Needs: o.Needs, KPIs: o.KPIs,
			Location: c4v1beta1.SystemLocation(o.Location), DomainRefs: o.Domains}}, nil
	case model.KindContainer:
		return &c4v1beta1.Container{ObjectMeta: objMeta, Spec: c4v1beta1.ContainerSpec{
			SoftwareSystemRef: c4v1beta1.LocalRef{Name: o.SoftwareSystem},
			DisplayName:       o.DisplayName, Description: o.Description,
			Type:           c4v1beta1.ContainerType(o.Type),
			GitProviderRef: c4v1beta1.LocalRef{Name: o.GitProvider},
			BundleRef:      optionalLocalRef(o.Bundle),
			FrameworkRef:   c4v1beta1.LocalRef{Name: o.Framework},
			StandardRef:    optionalLocalRef(o.Standard),
			ComputeRef:     optionalLocalRef(o.Compute),
			DeploymentRef:  optionalLocalRef(o.Deployment),
			Checks:         c4v1beta1.ChecksMode(o.Checks),
			Operations:     OperationsSpec(o.Operations),
		}}, nil
	case model.KindDataStore:
		return &c4v1beta1.DataStore{ObjectMeta: objMeta, Spec: c4v1beta1.DataStoreSpec{
			SoftwareSystemRef: c4v1beta1.LocalRef{Name: o.SoftwareSystem},
			DisplayName:       o.DisplayName, Description: o.Description,
			ConnectorRef: c4v1beta1.LocalRef{Name: o.Connector},
		}}, nil
	case model.KindRelationship:
		return &c4v1beta1.Relationship{ObjectMeta: objMeta, Spec: c4v1beta1.RelationshipSpec{
			Source:      c4v1beta1.ElementRef{Kind: c4v1beta1.ElementKind(o.Source.Kind), Name: o.Source.Name},
			Target:      c4v1beta1.ElementRef{Kind: c4v1beta1.ElementKind(o.Target.Kind), Name: o.Target.Name},
			Description: o.Description, Bundles: o.Bundles,
		}}, nil
	}
	return nil, fmt.Errorf("a plan doesn't write %s", o.Kind)
}

func optionalLocalRef(name string) *c4v1beta1.LocalRef {
	if name == "" {
		return nil
	}
	return &c4v1beta1.LocalRef{Name: name}
}

// CopyStoryText copies the story text a plan may change, displayName,
// description, needs and kpis, from src to dst, of the same kind, and a
// Container's checks when the plan sets it. A SoftwareSystem keeps its
// location: the round that created it set it, and turning a built system
// External is a person's edit, not a later plan's (#1150). It keeps its
// domainRefs too: its Domains are picked when it is made (#1760). Every other
// field of dst, such as a Container's adopt, license, checkCommand and
// deploy, stays as it is.
func CopyStoryText(dst, src APIObject) {
	switch d := dst.(type) {
	case *c4v1beta1.Project:
		s, _ := src.(*c4v1beta1.Project)
		d.Spec.DisplayName, d.Spec.Description = s.Spec.DisplayName, s.Spec.Description
	case *c4v1beta1.Person:
		s, _ := src.(*c4v1beta1.Person)
		d.Spec.DisplayName, d.Spec.Description = s.Spec.DisplayName, s.Spec.Description
	case *c4v1beta1.SoftwareSystem:
		s, _ := src.(*c4v1beta1.SoftwareSystem)
		d.Spec.DisplayName, d.Spec.Description = s.Spec.DisplayName, s.Spec.Description
		d.Spec.Needs, d.Spec.KPIs = s.Spec.Needs, s.Spec.KPIs
	case *c4v1beta1.Container:
		s, _ := src.(*c4v1beta1.Container)
		d.Spec.DisplayName, d.Spec.Description = s.Spec.DisplayName, s.Spec.Description
		if s.Spec.Checks != "" {
			d.Spec.Checks = s.Spec.Checks
		}
	case *c4v1beta1.DataStore:
		s, _ := src.(*c4v1beta1.DataStore)
		d.Spec.DisplayName, d.Spec.Description = s.Spec.DisplayName, s.Spec.Description
	case *c4v1beta1.Relationship:
		s, _ := src.(*c4v1beta1.Relationship)
		d.Spec.Description = s.Spec.Description
	}
}

// PlannedOperations is an existing Container's block as a plan's object
// carries it, nil when spec has none, so a plan applied again matches it.
func PlannedOperations(spec *c4v1beta1.OperationsSpec) *model.OperationsChoices {
	if spec == nil {
		return nil
	}
	c := OperationsChoices(spec)
	return &c
}

// OperationsSpec is a plan's block as spec.operations writes it: only the
// fields the plan set, so the factory's defaults stay its own; nil is nil.
func OperationsSpec(c *model.OperationsChoices) *c4v1beta1.OperationsSpec {
	if c == nil {
		return nil
	}
	spec := &c4v1beta1.OperationsSpec{}
	if c.RetentionDays != nil || c.CopyToRegion != "" {
		spec.Backups = &c4v1beta1.OperationsBackups{RetentionDays: c.RetentionDays, CopyToRegion: c.CopyToRegion}
	}
	if c.RPO != "" || c.RTO != "" {
		spec.Recovery = &c4v1beta1.OperationsRecovery{RPO: c.RPO, RTO: c.RTO}
	}
	if c.Email != "" {
		spec.Alerts = &c4v1beta1.OperationsAlerts{Email: c.Email}
	}
	return spec
}

// Catalog is every catalog entry of objs, side by side in layer order.
func Catalog(objs Objects) []model.CatalogEntry {
	out := make([]model.CatalogEntry, 0, len(objs.Frameworks)+len(objs.Standards)+len(objs.Computes)+
		len(objs.Deployments)+len(objs.Connectors)+len(objs.Domains))
	for i := range objs.Frameworks {
		out = append(out, CatalogEntry(model.SideFramework, &objs.Frameworks[i]))
	}
	for i := range objs.Standards {
		out = append(out, CatalogEntry(model.SideStandard, &objs.Standards[i]))
	}
	for i := range objs.Computes {
		out = append(out, CatalogEntry(model.SideCompute, &objs.Computes[i]))
	}
	for i := range objs.Deployments {
		out = append(out, CatalogEntry(model.SideDeployment, &objs.Deployments[i]))
	}
	for i := range objs.Connectors {
		out = append(out, CatalogEntry(model.SideConnector, &objs.Connectors[i]))
	}
	for i := range objs.Domains {
		out = append(out, CatalogEntry(model.SideDomain, &objs.Domains[i]))
	}
	return out
}

// Bundles are the Bundles as the entry names each picks, the way the
// operator expands a Container's bundleRef.
func Bundles(bundles []c4v1beta1.Bundle) []model.Bundle {
	out := make([]model.Bundle, 0, len(bundles))
	for _, b := range bundles {
		out = append(out, model.Bundle{Name: b.Name, Description: b.Spec.Description, Framework: b.Spec.FrameworkRef.Name,
			Standard: refName(b.Spec.StandardRef), Compute: refName(b.Spec.ComputeRef),
			Deployment: refName(b.Spec.DeploymentRef)})
	}
	return out
}

// planLabelColor is the color of a label a plan asks for that the factory
// doesn't define.
const planLabelColor = "#ededed"

// IssueLabels are the factory's labels, as a repository's issues need them.
func IssueLabels() []ports.Label {
	labels := append(model.StatusLabels(), model.UrgentLabel(), model.RiskLabel())
	out := make([]ports.Label, 0, len(labels))
	for _, l := range labels {
		out = append(out, ports.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
	}
	return out
}

// PlanIssueLabels are the labels a plan's issues need: the factory's, and
// any other the plan asked for.
func PlanIssueLabels(issues []model.PlanIssue) []ports.Label {
	labels := IssueLabels()
	for _, issue := range issues {
		for _, name := range model.BriefIssueLabels(issue.Labels) {
			switch {
			case slices.ContainsFunc(labels, func(l ports.Label) bool { return l.Name == name }):
			case name == model.LabelByHand:
				l := model.ByHandLabel()
				labels = append(labels,
					ports.Label{Name: l.Name, Color: l.Color, Description: l.Description, Exclusive: l.Exclusive})
			default:
				labels = append(labels, ports.Label{Name: name, Color: planLabelColor})
			}
		}
	}
	return labels
}
