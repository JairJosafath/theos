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

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// resolved is everything a Container's scaffold depends on, turned into the plain
// values the business rules take.
type resolved struct {
	provider *c4v1beta1.GitProvider
	// creds come from the GitProvider's credentials Secret.
	creds gitprovider.Credentials
	// entries are the catalog entries in layer order: Framework, Standard,
	// Compute, Deployment, then each Connector.
	entries []model.CatalogEntry
	// standard is the Standard's entry, or nil when the Container picks none.
	standard *model.CatalogEntry
	// use is what the Container picks up through its Relationships to DataStores.
	use model.DataStoreUse
	// choices are the entries the Container picked, by name: its spec's refs,
	// or its Bundle's.
	choices c4v1beta1.ContainerChoices
	// bundle is what the Container's Bundle expanded to, or nil when it names
	// none or the Bundle is missing.
	bundle *c4v1beta1.ContainerBundleStatus
}

// resolve looks up every object the Container's scaffold needs. missing names the
// ones that don't exist yet, such as "Framework python-fastapi".
func (r *ContainerReconciler) resolve(ctx context.Context, container *c4v1beta1.Container) (resolved, []string, error) {
	var res resolved
	var missing []string
	ns := container.Namespace
	spec := container.Spec

	var provider c4v1beta1.GitProvider
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

	// A Bundle is expanded afresh at each resolve, as if the Container had
	// named its entries; only a Container not yet scaffolded resolves.
	res.choices = container.Choices()
	if spec.BundleRef != nil {
		res.choices = c4v1beta1.ContainerChoices{Bundle: spec.BundleRef.Name}
		var bundle c4v1beta1.Bundle
		found, err := r.lookup(ctx, ns, spec.BundleRef.Name, &bundle)
		if err != nil {
			return res, nil, err
		}
		if found {
			b := bundle.Spec
			res.bundle = &c4v1beta1.ContainerBundleStatus{Name: bundle.Name, Framework: b.FrameworkRef.Name,
				Standard: refName(b.StandardRef), Compute: refName(b.ComputeRef), Deployment: refName(b.DeploymentRef)}
			res.choices = c4v1beta1.ContainerChoices{Bundle: bundle.Name, Framework: res.bundle.Framework,
				Standard: res.bundle.Standard, Compute: res.bundle.Compute, Deployment: res.bundle.Deployment}
		} else {
			missing = append(missing, "Bundle "+spec.BundleRef.Name)
		}
	}

	objs := map[string]client.Object{
		model.SideFramework:  &c4v1beta1.Framework{},
		model.SideStandard:   &c4v1beta1.Standard{},
		model.SideCompute:    &c4v1beta1.Compute{},
		model.SideDeployment: &c4v1beta1.Deployment{},
	}
	for _, c := range model.BundleChoices(model.Bundle{Framework: res.choices.Framework,
		Standard: res.choices.Standard, Compute: res.choices.Compute, Deployment: res.choices.Deployment}) {
		obj := objs[c.Side]
		found, err := r.lookup(ctx, ns, c.Name, obj)
		if err != nil {
			return res, nil, err
		}
		if !found {
			missing = append(missing, c.Side+" "+c.Name)
			continue
		}
		entry := catalogEntry(c.Side, obj)
		res.entries = append(res.entries, entry)
		if c.Side == model.SideStandard {
			res.standard = &entry
		}
	}

	// Connectors come through Relationships from this Container to DataStores.
	var rels c4v1beta1.RelationshipList
	if err := r.List(ctx, &rels, client.InNamespace(ns),
		client.MatchingFields{indexSource: elementKey(c4v1beta1.ElementContainer, container.Name)}); err != nil {
		return res, nil, err
	}
	var stores c4v1beta1.DataStoreList
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
		var connector c4v1beta1.Connector
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
func (r *ContainerReconciler) credentials(ctx context.Context, provider *c4v1beta1.GitProvider) (gitprovider.Credentials, bool, error) {
	return readCredentials(ctx, r.APIReader, provider)
}

// readCredentials reads a GitProvider's Secret without caching it.
func readCredentials(ctx context.Context, apiReader client.Reader, provider *c4v1beta1.GitProvider) (gitprovider.Credentials, bool, error) {
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
	var spec c4v1beta1.CatalogEntrySpec
	var frameworks []string
	var checksBeforeRelease, deploysItself, readsOperations bool
	switch o := obj.(type) {
	case *c4v1beta1.Framework:
		spec = o.Spec.CatalogEntrySpec
		deploysItself = o.Spec.DeploysItself
		readsOperations = o.Spec.ReadsOperations
	case *c4v1beta1.Standard:
		spec = o.Spec.CatalogEntrySpec
		frameworks = o.Spec.SupportedFrameworks
		checksBeforeRelease = o.Spec.ChecksBeforeRelease
	case *c4v1beta1.Compute:
		spec = o.Spec
	case *c4v1beta1.Deployment:
		spec = o.Spec
	case *c4v1beta1.Connector:
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
		DeploysItself:       deploysItself,
		ReadsOperations:     readsOperations,
	}
}
