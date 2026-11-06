/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
)

// Field indexes the reconcilers use to find related objects in the cache.
const (
	// indexSoftwareSystem indexes Containers and DataStores by their SoftwareSystem.
	indexSoftwareSystem = ".spec.softwareSystemRef.name"
	// indexContainer indexes Sessions by their Container.
	indexContainer = ".spec.containerRef.name"
	// indexTaskContainer indexes Tasks by their Container.
	indexTaskContainer = indexContainer
	// indexTaskRepository indexes Tasks by the repository they work in (a
	// Container's, or a Plan Task's Brief's), and by their issue,
	// Standard-change issue and pull request as <repository>#<number> and
	// their branches as <repository>@<branch>, for the host's deliveries
	// (docs/plans/done/git-events.md, decision 7).
	indexTaskRepository = ".taskRepository"
	// indexElement indexes Relationships by both ends, as "Kind/name".
	indexElement = ".spec.elements"
	// indexSource indexes Relationships by their source, as "Kind/name".
	indexSource = ".spec.source"
)

// elementKey is how Relationship ends appear in the indexes.
func elementKey(kind c4v1beta1.ElementKind, name string) string {
	return string(kind) + "/" + name
}

// SetupIndexes registers the field indexes. Call it once, before the reconcilers.
func SetupIndexes(ctx context.Context, mgr manager.Manager) error {
	indexer := mgr.GetFieldIndexer()
	if err := indexer.IndexField(ctx, &c4v1beta1.Container{}, indexSoftwareSystem,
		func(o client.Object) []string {
			return []string{o.(*c4v1beta1.Container).Spec.SoftwareSystemRef.Name}
		}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &c4v1beta1.DataStore{}, indexSoftwareSystem,
		func(o client.Object) []string {
			return []string{o.(*c4v1beta1.DataStore).Spec.SoftwareSystemRef.Name}
		}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &c4v1beta1.Session{}, indexContainer,
		func(o client.Object) []string {
			return []string{o.(*c4v1beta1.Session).Spec.ContainerRef.Name}
		}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &c4v1beta1.Crew{}, indexContainer,
		func(o client.Object) []string {
			return []string{o.(*c4v1beta1.Crew).Spec.ContainerRef.Name}
		}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &c4v1beta1.Task{}, indexTaskContainer,
		func(o client.Object) []string {
			// A Plan Task names a Brief, not a Container.
			if name := o.(*c4v1beta1.Task).Spec.ContainerRef.Name; name != "" {
				return []string{name}
			}
			return nil
		}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &c4v1beta1.Task{}, indexTaskRepository,
		func(o client.Object) []string { return taskRepositoryKeys(o.(*c4v1beta1.Task)) }); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &c4v1beta1.Relationship{}, indexSource,
		func(o client.Object) []string {
			rel := o.(*c4v1beta1.Relationship)
			return []string{elementKey(rel.Spec.Source.Kind, rel.Spec.Source.Name)}
		}); err != nil {
		return err
	}
	return indexer.IndexField(ctx, &c4v1beta1.Relationship{}, indexElement,
		func(o client.Object) []string {
			rel := o.(*c4v1beta1.Relationship)
			return []string{
				elementKey(rel.Spec.Source.Kind, rel.Spec.Source.Name),
				elementKey(rel.Spec.Target.Kind, rel.Spec.Target.Name),
			}
		})
}
