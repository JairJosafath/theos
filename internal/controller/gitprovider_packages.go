/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/
package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// packagesOf is a GitProvider's spec.packages as the Provider's settings:
// the registries it names, with "" for each it leaves to the host's own.
// The tokens are not here: packagesWithTokens adds the ones that publish.
func packagesOf(gp *c4v1beta1.GitProvider) gitprovider.Packages {
	var p gitprovider.Packages
	if gp.Spec.Packages == nil {
		return p
	}
	if npm := gp.Spec.Packages.NPM; npm != nil {
		p.NpmScope, p.NpmRegistry = npm.Scope, npm.Registry
	}
	if pypi := gp.Spec.Packages.PyPI; pypi != nil {
		p.PyPIIndex, p.PyPIUpload = pypi.Index, pypi.Upload
	}
	return p
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
	gitprovider.Packages, error) {
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
