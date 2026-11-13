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

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// StorageMigratorPageSize is how many objects one list page of the migrator
// holds.
const StorageMigratorPageSize = 500

// StorageMigratorRetry is how long the migrator waits after a failed pass
// before the next.
const StorageMigratorRetry = 30 * time.Second

// StorageMigratorReadyPoll is how often the migrator asks Ready before the
// pass.
const StorageMigratorReadyPoll = time.Second

// +kubebuilder:rbac:groups=theos.factory.io,resources=agents;benchmarks;briefs;bundles;chats;computes;connectors;containers;crews;datastores;deployments;deploytargets;domains;frameworks;gitproviders;people;projects;relationships;scorers;sessions;softwaresystems;standards;tasks;testenvironments,verbs=list;patch
// +kubebuilder:rbac:groups=apiextensions.k8s.io,resources=customresourcedefinitions/status,verbs=patch,resourceNames=agents.theos.factory.io;benchmarks.theos.factory.io;briefs.theos.factory.io;bundles.theos.factory.io;chats.theos.factory.io;computes.theos.factory.io;connectors.theos.factory.io;containers.theos.factory.io;crews.theos.factory.io;datastores.theos.factory.io;deployments.theos.factory.io;deploytargets.theos.factory.io;domains.theos.factory.io;frameworks.theos.factory.io;gitproviders.theos.factory.io;people.theos.factory.io;projects.theos.factory.io;relationships.theos.factory.io;scorers.theos.factory.io;sessions.theos.factory.io;softwaresystems.theos.factory.io;standards.theos.factory.io;tasks.theos.factory.io;testenvironments.theos.factory.io

// StorageMigrator rewrites what etcd holds at a version other than the
// storage one (docs/plans/done/api-versions.md, decision 4). For each CRD in CRDs
// whose status.storedVersions names a version besides the storage version, it
// lists the objects at the storage version, page by page, issues an empty
// patch per object, so the API server writes each back at the storage
// version, and then patches storedVersions down to the storage version alone.
// An object that won't rewrite is logged and skipped, and its CRD keeps its
// storedVersions, so the next pass tries it again; the other CRDs go on. It
// is idempotent: a CRD already storing one version is skipped, so a start
// after the pass does nothing. As a manager Runnable it runs under leader
// election, whatever the webhook setting, and tries again every Retry until a
// pass succeeds; a CRD that converts by the webhook (strategy Webhook) waits
// for Ready before its objects are rewritten, one at strategy None does not
// (#1884).
type StorageMigrator struct {
	// Client reads and writes without a cache: the pass is one pull, and the
	// caches of the kinds it rewrites may not be started.
	Client client.Client
	// CRDs defaults to WebhookCRDs.
	CRDs []string
	// Ready, when set, is waited on before a CRD at strategy Webhook is
	// rewritten (the webhook server's started check): conversion of its
	// stored objects needs it. A CRD at strategy None never waits; nil, with
	// the webhook off, means no CRD does.
	Ready func(*http.Request) error
	// PageSize defaults to StorageMigratorPageSize.
	PageSize int64
	// Retry defaults to StorageMigratorRetry.
	Retry time.Duration
	// ReadyPoll defaults to StorageMigratorReadyPoll.
	ReadyPoll time.Duration
}

// NeedLeaderElection is true: the leader rewrites the objects; without leader
// election every replica does, idempotently.
func (m *StorageMigrator) NeedLeaderElection() bool { return true }

// Start runs Migrate until a pass succeeds or ctx ends. It returns nil either
// way: a pass that keeps failing is logged, and the manager runs on with the
// stored objects as they are, converted on read.
func (m *StorageMigrator) Start(ctx context.Context) error {
	log := logf.FromContext(ctx).WithName("storage-migrator")
	retry := m.Retry
	if retry <= 0 {
		retry = StorageMigratorRetry
	}
	for {
		err := m.Migrate(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
		log.Error(err, "Failed to migrate the stored objects; trying again", "after", retry.String())
		if !sleep(ctx, retry) {
			return nil
		}
	}
}

// sleep waits d, or until ctx ends; it is true when d passed.
func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// waitReady waits, every ReadyPoll, until Ready passes or ctx ends; it
// returns ctx's error in the latter case.
func (m *StorageMigrator) waitReady(ctx context.Context) error {
	poll := m.ReadyPoll
	if poll <= 0 {
		poll = StorageMigratorReadyPoll
	}
	for m.Ready != nil && m.Ready(nil) != nil {
		if !sleep(ctx, poll) {
			return ctx.Err()
		}
	}
	return nil
}

// Migrate runs one pass over CRDs, as described on StorageMigrator. A CRD
// that fails doesn't stop the others; the errors come back joined.
func (m *StorageMigrator) Migrate(ctx context.Context) error {
	names := m.CRDs
	if names == nil {
		names = WebhookCRDs
	}
	var errs []error
	for _, name := range names {
		if err := m.migrateCRD(ctx, name); err != nil {
			errs = append(errs, err)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return errors.Join(errs...)
}

// migrateCRD rewrites the objects of the CRD name when its storedVersions
// lists a version besides the storage one, and patches storedVersions down
// when they are all done.
func (m *StorageMigrator) migrateCRD(ctx context.Context, name string) error {
	log := logf.FromContext(ctx).WithName("storage-migrator").WithValues("crd", name)
	var crd apiextensionsv1.CustomResourceDefinition
	if err := m.Client.Get(ctx, client.ObjectKey{Name: name}, &crd); err != nil {
		return fmt.Errorf("read CRD %s: %w", name, err)
	}
	i := slices.IndexFunc(crd.Spec.Versions, func(v apiextensionsv1.CustomResourceDefinitionVersion) bool {
		return v.Storage
	})
	if i < 0 {
		return fmt.Errorf("CRD %s has no storage version", name)
	}
	storage := crd.Spec.Versions[i].Name
	if !slices.ContainsFunc(crd.Status.StoredVersions, func(v string) bool { return v != storage }) {
		return nil
	}
	if crd.Spec.Conversion != nil && crd.Spec.Conversion.Strategy == apiextensionsv1.WebhookConverter {
		// Rewriting goes through the conversion webhook: not before it serves.
		// A CRD at strategy None converts in the API server and never waits
		// (#1884).
		if err := m.waitReady(ctx); err != nil {
			return fmt.Errorf("wait for the conversion webhook before rewriting %s: %w", crd.Spec.Names.Plural, err)
		}
	}
	log.Info("Rewriting the stored objects at the storage version",
		"storedVersions", crd.Status.StoredVersions, "storage", storage)

	gvk := schema.GroupVersionKind{Group: crd.Spec.Group, Version: storage, Kind: crd.Spec.Names.ListKind}
	size := m.PageSize
	if size <= 0 {
		size = StorageMigratorPageSize
	}
	patched, failed := 0, 0
	for token := ""; ; {
		var list unstructured.UnstructuredList
		list.SetGroupVersionKind(gvk)
		err := m.Client.List(ctx, &list, client.Limit(size), client.Continue(token))
		if apierrors.IsResourceExpired(err) && token != "" {
			// The continue token outlived etcd's compaction window: start the
			// list over; the objects already rewritten are patched again,
			// which changes nothing.
			log.Info("The list's continue token expired; listing again from the start")
			token = ""
			continue
		}
		if err != nil {
			return fmt.Errorf("list %s at %s: %w", crd.Spec.Names.Plural, storage, err)
		}
		for i := range list.Items {
			obj := &list.Items[i]
			err := m.Client.Patch(ctx, obj, client.RawPatch(types.MergePatchType, []byte("{}")))
			if apierrors.IsNotFound(err) {
				continue
			}
			if err != nil {
				if ctx.Err() != nil {
					return fmt.Errorf("rewrite %s %s/%s: %w", crd.Spec.Names.Kind, obj.GetNamespace(), obj.GetName(), err)
				}
				failed++
				log.Error(err, "Failed to rewrite a stored object; carrying on with the others",
					"kind", crd.Spec.Names.Kind, "namespace", obj.GetNamespace(), "name", obj.GetName())
				continue
			}
			patched++
		}
		token = list.GetContinue()
		if token == "" {
			break
		}
	}
	if failed > 0 {
		return fmt.Errorf("rewrite %d of %d %s at %s", failed, failed+patched, crd.Spec.Names.Plural, storage)
	}

	// The CRD read at the top is the base, with its resourceVersion: a CRD
	// changed since (a version added, storedVersions touched) refuses the
	// patch, and the next pass reads it again. What stays open is an API
	// server that writes an object at the old version after the list
	// snapshot, before it sees the new storage version; upstream's migrator
	// accepts the same window, and a later pass can't see it either.
	base := crd.DeepCopy()
	crd.Status.StoredVersions = []string{storage}
	if err := m.Client.Status().Patch(ctx, &crd, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("set CRD %s storedVersions to %s: %w", name, storage, err)
	}
	log.Info("Rewrote the stored objects at the storage version", "objects", patched, "storage", storage)
	return nil
}
