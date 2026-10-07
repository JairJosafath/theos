/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"slices"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// +kubebuilder:rbac:groups=c4.factory.io,resources=people;relationships;datastores,verbs=get;list;watch

// c4Context gathers a Container's place in the story for its agents: its system,
// the system's users, and the Container's Relationships with their other ends.
// Missing elements are left out: the context informs, it doesn't gate.
func c4Context(ctx context.Context, c client.Client, container *c4v1alpha1.Container) model.C4Context {
	ns := container.Namespace
	out := model.C4Context{Container: model.C4Element{Kind: "Container", Name: container.Name,
		DisplayName: container.Spec.DisplayName, Description: container.Spec.Description, Type: string(container.Spec.Type)}}

	var system c4v1alpha1.SoftwareSystem
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: container.Spec.SoftwareSystemRef.Name}, &system); err == nil {
		out.System = model.C4Element{Kind: "SoftwareSystem", Name: system.Name,
			DisplayName: system.Spec.DisplayName, Description: system.Spec.Description}
		out.Needs, out.KPIs = system.Spec.Needs, system.Spec.KPIs
		var rels c4v1alpha1.RelationshipList
		if err := c.List(ctx, &rels, client.InNamespace(ns),
			client.MatchingFields{indexElement: elementKey(c4v1alpha1.ElementSoftwareSystem, system.Name)}); err == nil {
			for _, rel := range sortedRelationships(rels.Items) {
				if rel.Spec.Source.Kind == c4v1alpha1.ElementPerson && rel.Spec.Target.Name == system.Name {
					out.Users = append(out.Users, storyElement(ctx, c, ns, rel.Spec.Source))
				}
			}
		}
	}

	var rels c4v1alpha1.RelationshipList
	if err := c.List(ctx, &rels, client.InNamespace(ns),
		client.MatchingFields{indexElement: elementKey(c4v1alpha1.ElementContainer, container.Name)}); err != nil {
		return out
	}
	for _, rel := range sortedRelationships(rels.Items) {
		outgoing := rel.Spec.Source.Kind == c4v1alpha1.ElementContainer && rel.Spec.Source.Name == container.Name
		other := rel.Spec.Source
		if outgoing {
			other = rel.Spec.Target
		}
		link := model.C4Link{Outgoing: outgoing, Other: storyElement(ctx, c, ns, other), Description: rel.Spec.Description}
		if other.Kind == c4v1alpha1.ElementContainer {
			var related c4v1alpha1.Container
			if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: other.Name}, &related); err == nil {
				link.RepositoryURL = related.Status.RepositoryURL
			}
		}
		out.Links = append(out.Links, link)
	}
	return out
}

// storyElement describes one end of a Relationship; just its kind and name when it
// can't be read.
func storyElement(ctx context.Context, c client.Client, ns string, ref c4v1alpha1.ElementRef) model.C4Element {
	e := model.C4Element{Kind: string(ref.Kind), Name: ref.Name}
	key := client.ObjectKey{Namespace: ns, Name: ref.Name}
	switch ref.Kind {
	case c4v1alpha1.ElementPerson:
		var p c4v1alpha1.Person
		if c.Get(ctx, key, &p) == nil {
			e.DisplayName, e.Description = p.Spec.DisplayName, p.Spec.Description
		}
	case c4v1alpha1.ElementSoftwareSystem:
		var s c4v1alpha1.SoftwareSystem
		if c.Get(ctx, key, &s) == nil {
			e.DisplayName, e.Description = s.Spec.DisplayName, s.Spec.Description
		}
	case c4v1alpha1.ElementContainer:
		var other c4v1alpha1.Container
		if c.Get(ctx, key, &other) == nil {
			e.DisplayName, e.Description, e.Type = other.Spec.DisplayName, other.Spec.Description, string(other.Spec.Type)
		}
	case c4v1alpha1.ElementDataStore:
		var d c4v1alpha1.DataStore
		if c.Get(ctx, key, &d) == nil {
			e.DisplayName, e.Description = d.Spec.DisplayName, d.Spec.Description
		}
	}
	return e
}

func sortedRelationships(rels []c4v1alpha1.Relationship) []c4v1alpha1.Relationship {
	return slices.SortedFunc(slices.Values(rels), func(a, b c4v1alpha1.Relationship) int {
		return strings.Compare(a.Name, b.Name)
	})
}
