/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// resolved is everything a Container's scaffold depends on, turned into the plain
// values the business rules take.
type resolved struct {
	provider *c4v1alpha1.GitProvider
	// creds come from the GitProvider's credentials Secret.
	creds gitprovider.Credentials
	// entries are the catalog entries in layer order: Framework, Standard,
	// Compute, Deployment, then each Connector.
	entries []model.CatalogEntry
	// standard is the Standard's entry, or nil when the Container picks none.
	standard *model.CatalogEntry
	// use is what the Container picks up through its Relationships to DataStores.
	use model.DataStoreUse
}

// resolve looks up every object the Container's scaffold needs. missing names the
// ones that don't exist yet, such as "Framework python-fastapi".
func (r *ContainerReconciler) resolve(ctx context.Context, container *c4v1alpha1.Container) (resolved, []string, error) {
	var res resolved
	var missing []string
	ns := container.Namespace
	spec := container.Spec

	var provider c4v1alpha1.GitProvider
	found, err := r.lookup(ctx, ns, spec.GitProviderRef.Name, &provider)
	if err != nil {
		return res, nil, err
	}
	if found {
		res.provider = &provider
		creds, ok, err := r.credentials(ctx, &provider)
		if err != nil {
			return res, nil, err
		}
		if ok {
			res.creds = creds
		} else {
			missing = append(missing, "Secret "+provider.Spec.CredentialsSecretRef.Name)
		}
	} else {
		missing = append(missing, "GitProvider "+spec.GitProviderRef.Name)
	}

	type sideRef struct {
		side string
		ref  *c4v1alpha1.LocalRef
		obj  client.Object
	}
	sides := []sideRef{
		{model.SideFramework, &spec.FrameworkRef, &c4v1alpha1.Framework{}},
		{model.SideStandard, spec.StandardRef, &c4v1alpha1.Standard{}},
		{model.SideCompute, spec.ComputeRef, &c4v1alpha1.Compute{}},
		{model.SideDeployment, spec.DeploymentRef, &c4v1alpha1.Deployment{}},
	}
	for _, s := range sides {
		if s.ref == nil {
			continue
		}
		found, err := r.lookup(ctx, ns, s.ref.Name, s.obj)
		if err != nil {
			return res, nil, err
		}
		if !found {
			missing = append(missing, s.side+" "+s.ref.Name)
			continue
		}
		entry := catalogEntry(s.side, s.obj)
		res.entries = append(res.entries, entry)
		if s.side == model.SideStandard {
			res.standard = &entry
		}
	}

	// Connectors come through Relationships from this Container to DataStores.
	var rels c4v1alpha1.RelationshipList
	if err := r.List(ctx, &rels, client.InNamespace(ns),
		client.MatchingFields{indexSource: elementKey(c4v1alpha1.ElementContainer, container.Name)}); err != nil {
		return res, nil, err
	}
	var stores c4v1alpha1.DataStoreList
	if err := r.List(ctx, &stores, client.InNamespace(ns)); err != nil {
		return res, nil, err
	}
	connectorOf := make(map[string]string, len(stores.Items))
	for _, s := range stores.Items {
		connectorOf[s.Name] = s.Spec.ConnectorRef.Name
	}
	modelRels := make([]model.Relationship, 0, len(rels.Items))
	for _, rel := range rels.Items {
		if rel.DeletionTimestamp.IsZero() {
			modelRels = append(modelRels, toModel(rel))
		}
	}
	res.use = model.DataStoresUsedBy(container.Name, modelRels, connectorOf)
	for _, name := range res.use.MissingDataStores {
		missing = append(missing, "DataStore "+name)
	}
	for _, name := range res.use.Connectors {
		var connector c4v1alpha1.Connector
		found, err := r.lookup(ctx, ns, name, &connector)
		if err != nil {
			return res, nil, err
		}
		if !found {
			missing = append(missing, "Connector "+name)
			continue
		}
		res.entries = append(res.entries, catalogEntry(model.SideConnector, &connector))
	}
	return res, missing, nil
}

// credentials reads a GitProvider's Secret. It goes straight to the API server
// rather than through the cache, so the operator never caches every Secret.
func (r *ContainerReconciler) credentials(ctx context.Context, provider *c4v1alpha1.GitProvider) (gitprovider.Credentials, bool, error) {
	return readCredentials(ctx, r.APIReader, provider)
}

// readCredentials reads a GitProvider's Secret without caching it.
func readCredentials(ctx context.Context, apiReader client.Reader, provider *c4v1alpha1.GitProvider) (gitprovider.Credentials, bool, error) {
	var secret corev1.Secret
	err := apiReader.Get(ctx, types.NamespacedName{
		Namespace: provider.Namespace, Name: provider.Spec.CredentialsSecretRef.Name,
	}, &secret)
	if apierrors.IsNotFound(err) {
		return gitprovider.Credentials{}, false, nil
	}
	if err != nil {
		return gitprovider.Credentials{}, false, err
	}
	return gitprovider.Credentials{
		Username: string(secret.Data["username"]),
		Token:    string(secret.Data["token"]),
	}, true, nil
}

// lookup gets a named object and reports whether it exists.
func (r *ContainerReconciler) lookup(ctx context.Context, ns, name string, obj client.Object) (bool, error) {
	err := r.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, obj)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// catalogEntry converts any of the five catalog kinds into a model.CatalogEntry.
func catalogEntry(side string, obj client.Object) model.CatalogEntry {
	var spec c4v1alpha1.CatalogEntrySpec
	var frameworks []string
	var checksBeforeRelease bool
	switch o := obj.(type) {
	case *c4v1alpha1.Framework:
		spec = o.Spec
	case *c4v1alpha1.Standard:
		spec = o.Spec.CatalogEntrySpec
		frameworks = o.Spec.SupportedFrameworks
		checksBeforeRelease = o.Spec.ChecksBeforeRelease
	case *c4v1alpha1.Compute:
		spec = o.Spec
	case *c4v1alpha1.Deployment:
		spec = o.Spec
	case *c4v1alpha1.Connector:
		spec = o.Spec
	}
	supported := make([]string, 0, len(spec.SupportedTypes))
	for _, t := range spec.SupportedTypes {
		supported = append(supported, string(t))
	}
	return model.CatalogEntry{
		Side:                side,
		Name:                obj.GetName(),
		Description:         spec.Description,
		SupportedTypes:      supported,
		TemplateURL:         spec.Template.URL,
		TemplateRef:         spec.Template.Ref,
		SupportedFrameworks: frameworks,
		ChecksBeforeRelease: checksBeforeRelease,
	}
}
