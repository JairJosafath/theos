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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
)

// resolve reads the objects the Container's scaffold needs and works out
// what they resolve to (modelfacts.Resolve). missing names the ones that
// don't exist yet, such as "Framework python-fastapi".
func (r *ContainerReconciler) resolve(ctx context.Context, container *c4v1beta1.Container,
	system *c4v1beta1.SoftwareSystem) (modelfacts.Resolved, []string, error) {
	objs, err := r.scaffoldObjects(ctx, container)
	if err != nil {
		return modelfacts.Resolved{}, nil, err
	}
	res, missing := modelfacts.Resolve(container, system, objs)
	return res, missing, nil
}

// scaffoldObjects lists the Container's namespace's objects of each kind its
// scaffold reads, and the credentials of its GitProvider's Secret.
func (r *ContainerReconciler) scaffoldObjects(ctx context.Context,
	container *c4v1beta1.Container) (modelfacts.Objects, error) {
	var objs modelfacts.Objects
	in := client.InNamespace(container.Namespace)
	var (
		providers   c4v1beta1.GitProviderList
		bundles     c4v1beta1.BundleList
		frameworks  c4v1beta1.FrameworkList
		standards   c4v1beta1.StandardList
		computes    c4v1beta1.ComputeList
		deployments c4v1beta1.DeploymentList
		connectors  c4v1beta1.ConnectorList
		domains     c4v1beta1.DomainList
		stores      c4v1beta1.DataStoreList
		rels        c4v1beta1.RelationshipList
	)
	for _, list := range []client.ObjectList{&providers, &bundles, &frameworks, &standards, &computes,
		&deployments, &connectors, &domains, &stores} {
		if err := r.List(ctx, list, in); err != nil {
			return objs, err
		}
	}
	// Relationships are read from the API server, not the cache: the
	// Relationship informer may not yet hold one created just before the
	// Container, and a scaffold missing it is never redone (#1992). A missing
	// DataStore or Connector only delays the scaffold, so those stay cached.
	if err := r.APIReader.List(ctx, &rels, in); err != nil {
		return objs, err
	}
	objs = modelfacts.Objects{GitProviders: providers.Items, Bundles: bundles.Items, Frameworks: frameworks.Items,
		Standards: standards.Items, Computes: computes.Items, Deployments: deployments.Items,
		Connectors: connectors.Items, Domains: domains.Items, DataStores: stores.Items, Relationships: rels.Items}
	if provider := modelfacts.Find(objs.GitProviders, container.Spec.GitProviderRef.Name); provider != nil {
		creds, ok, err := r.credentials(ctx, provider)
		if err != nil {
			return objs, err
		}
		if ok {
			objs.Secrets = map[string]ports.Credentials{provider.Spec.CredentialsSecretRef.Name: creds}
		}
	}
	return objs, nil
}

// credentials reads a GitProvider's Secret. It goes straight to the API server
// rather than through the cache, so the operator never caches every Secret.
func (r *ContainerReconciler) credentials(ctx context.Context, provider *c4v1beta1.GitProvider) (ports.Credentials, bool, error) {
	return readCredentials(ctx, r.APIReader, provider)
}

// readCredentials reads a GitProvider's Secret without caching it.
func readCredentials(ctx context.Context, apiReader client.Reader, provider *c4v1beta1.GitProvider) (ports.Credentials, bool, error) {
	var secret corev1.Secret
	err := apiReader.Get(ctx, types.NamespacedName{
		Namespace: provider.Namespace, Name: provider.Spec.CredentialsSecretRef.Name,
	}, &secret)
	if apierrors.IsNotFound(err) {
		return ports.Credentials{}, false, nil
	}
	if err != nil {
		return ports.Credentials{}, false, err
	}
	return ports.Credentials{
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
