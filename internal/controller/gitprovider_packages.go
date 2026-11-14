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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
)

// packagesOf is a GitProvider's spec.packages as the Provider's settings:
// the registries it names, with "" for each it leaves to the host's own.
// The tokens are not here: packagesWithTokens adds the ones that publish.
// It forwards to modelfacts.Packages and stays as the reconcilers' and their
// unit tests' name for it, so this part (#268) moves no call site.
func packagesOf(gp *c4v1beta1.GitProvider) ports.Packages {
	return modelfacts.Packages(gp)
}

// packagesSecrets names the Secrets of a GitProvider's npm registry and
// PyPI index, "" for one without.
func packagesSecrets(gp *c4v1beta1.GitProvider) (npm, pypi string) {
	if gp.Spec.Packages == nil {
		return "", ""
	}
	if n := gp.Spec.Packages.NPM; n != nil && n.CredentialsSecretRef != nil {
		npm = n.CredentialsSecretRef.Name
	}
	if p := gp.Spec.Packages.PyPI; p != nil && p.CredentialsSecretRef != nil {
		pypi = p.CredentialsSecretRef.Name
	}
	return npm, pypi
}

// packagesTokenKey is the key of a package registry's Secret that publishes;
// only the operator reads it.
const packagesTokenKey = "token"

// packagesWithTokens is packagesOf(gp) with the token of each registry Secret
// gp names, read straight from the API server: the Crew publishes with it to
// a registry on another host (#1729). No Secret, or none with the key, leaves
// the token "", which publishes with the host's own credentials.
func packagesWithTokens(ctx context.Context, apiReader client.Reader, gp *c4v1beta1.GitProvider) (
	ports.Packages, error) {
	p := packagesOf(gp)
	npm, pypi := packagesSecrets(gp)
	var err error
	if p.NpmToken, err = secretKey(ctx, apiReader, gp.Namespace, npm, packagesTokenKey); err != nil {
		return p, err
	}
	if p.PyPIToken, err = secretKey(ctx, apiReader, gp.Namespace, pypi, packagesTokenKey); err != nil {
		return p, err
	}
	return p, nil
}

// secretKey is key of the named Secret in ns, "" for no name, no Secret or no
// key.
func secretKey(ctx context.Context, apiReader client.Reader, ns, name, key string) (string, error) {
	if name == "" {
		return "", nil
	}
	var secret corev1.Secret
	err := apiReader.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, &secret)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("package registry Secret %s: %w", name, err)
	}
	return string(secret.Data[key]), nil
}
