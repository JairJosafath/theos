/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/
package controller

import (
	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// packagesOf is a GitProvider's spec.packages as the Provider's settings:
// the registries it names, with "" for each it leaves to the host's own.
// The tokens are not here: they are read from the Secrets by whoever
// publishes or installs.
func packagesOf(gp *c4v1alpha1.GitProvider) gitprovider.Packages {
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
func packagesSecrets(gp *c4v1alpha1.GitProvider) (npm, pypi string) {
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
