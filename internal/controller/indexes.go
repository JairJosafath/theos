/*
Copyright 2026.

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

package controller

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
)

// Field indexes the reconcilers use to find related objects in the cache.
const (
	// indexSoftwareSystem indexes Containers and DataStores by their SoftwareSystem.
	indexSoftwareSystem = ".spec.softwareSystemRef.name"
	// indexContainer indexes Sessions by their Container.
	indexContainer = ".spec.containerRef.name"
	// indexTaskContainer indexes Tasks by their Container.
	indexTaskContainer = indexContainer
	// indexElement indexes Relationships by both ends, as "Kind/name".
	indexElement = ".spec.elements"
	// indexSource indexes Relationships by their source, as "Kind/name".
	indexSource = ".spec.source"
)

// elementKey is how Relationship ends appear in the indexes.
func elementKey(kind c4v1alpha1.ElementKind, name string) string {
	return string(kind) + "/" + name
}

// SetupIndexes registers the field indexes. Call it once, before the reconcilers.
func SetupIndexes(ctx context.Context, mgr manager.Manager) error {
	indexer := mgr.GetFieldIndexer()
	if err := indexer.IndexField(ctx, &c4v1alpha1.Container{}, indexSoftwareSystem,
		func(o client.Object) []string {
			return []string{o.(*c4v1alpha1.Container).Spec.SoftwareSystemRef.Name}
		}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &c4v1alpha1.DataStore{}, indexSoftwareSystem,
		func(o client.Object) []string {
			return []string{o.(*c4v1alpha1.DataStore).Spec.SoftwareSystemRef.Name}
		}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &c4v1alpha1.Session{}, indexContainer,
		func(o client.Object) []string {
			return []string{o.(*c4v1alpha1.Session).Spec.ContainerRef.Name}
		}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &c4v1alpha1.Crew{}, indexContainer,
		func(o client.Object) []string {
			return []string{o.(*c4v1alpha1.Crew).Spec.ContainerRef.Name}
		}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &c4v1alpha1.Task{}, indexTaskContainer,
		func(o client.Object) []string {
			return []string{o.(*c4v1alpha1.Task).Spec.ContainerRef.Name}
		}); err != nil {
		return err
	}
	if err := indexer.IndexField(ctx, &c4v1alpha1.Relationship{}, indexSource,
		func(o client.Object) []string {
			rel := o.(*c4v1alpha1.Relationship)
			return []string{elementKey(rel.Spec.Source.Kind, rel.Spec.Source.Name)}
		}); err != nil {
		return err
	}
	return indexer.IndexField(ctx, &c4v1alpha1.Relationship{}, indexElement,
		func(o client.Object) []string {
			rel := o.(*c4v1alpha1.Relationship)
			return []string{
				elementKey(rel.Spec.Source.Kind, rel.Spec.Source.Name),
				elementKey(rel.Spec.Target.Kind, rel.Spec.Target.Name),
			}
		})
}
