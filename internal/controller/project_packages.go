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
	"maps"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// The owner's Actions variables and secrets that name its package
// registries, which the workflows of #1231 read; by default, when
// spec.packages leaves everything to the host's own registries, none is set
// (docs/plans/done/app-packages.md, decision 5).
const (
	ActionsVarNpmScope     = "C4_NPM_SCOPE"
	ActionsVarNpmRegistry  = "C4_NPM_REGISTRY"
	ActionsVarPyPIIndex    = "C4_PYPI_INDEX"
	ActionsSecretNpmToken  = "C4_NPM_TOKEN"  //nolint:gosec // the name of an Actions secret
	ActionsSecretPyPIToken = "C4_PYPI_TOKEN" //nolint:gosec // the name of an Actions secret
)

// packagesActions is what the owner's Actions hold for its package
// registries: the variables and the secrets, each empty for none.
type packagesActions struct {
	variables map[string]string
	secrets   map[string]string
}

func (a packagesActions) equal(b packagesActions) bool {
	return maps.Equal(a.variables, b.variables) && maps.Equal(a.secrets, b.secrets)
}

// ensurePackagesActions keeps, for every GitProvider of the project's
// namespace whose host has owner Actions, the owner's Actions variables
// (C4_NPM_SCOPE, C4_NPM_REGISTRY, C4_PYPI_INDEX) and secrets (C4_NPM_TOKEN
// and C4_PYPI_TOKEN, from the registry Secrets' readToken): set when
// spec.packages departs from the host's own registries, with the defaults
// filled in for what it leaves out, and deleted, all five, when it is the
// default. A Secret or readToken that is missing deletes its Actions secret.
// What was last applied is kept in memory per GitProvider, so an unchanged
// spec sends the host nothing; a restart sends it once more. A changed
// spec.packages reconciles the Project through its GitProvider watch; a
// changed readToken is applied on the Project's next reconcile.
func (r *ProjectReconciler) ensurePackagesActions(ctx context.Context, project *c4v1beta1.Project,
	providers []c4v1beta1.GitProvider) error {
	log := logf.FromContext(ctx)
	for i := range providers {
		gp := &providers[i]
		provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, project.Namespace, gp.Name)
		if err != nil {
			if !apierrors.IsNotFound(err) {
				log.Info("Skipped the package registries of a GitProvider the operator cannot reach",
					"gitProvider", gp.Name, "reason", err.Error())
			}
			continue
		}
		actions, ok := provider.(gitprovider.OwnerActions)
		if !ok {
			continue
		}
		want, err := r.packagesActionsOf(ctx, gp, provider)
		if err != nil {
			return err
		}
		memoKey := project.Namespace + "/" + gp.Name
		if last, ok := r.packagesApplied.Load(memoKey); ok && last.(packagesActions).equal(want) {
			continue
		}
		if err := applyPackagesActions(ctx, actions, want); err != nil {
			return fmt.Errorf("package registries of GitProvider %s: %w", gp.Name, err)
		}
		r.packagesApplied.Store(memoKey, want)
		log.Info("Applied the owner's package registry Actions variables and secrets",
			"gitProvider", gp.Name, "variables", len(want.variables), "secrets", len(want.secrets))
	}
	return nil
}

// packagesActionsOf is what gp's owner Actions should hold: nothing for the
// default spec.packages, else the three variables from the provider's
// addresses and each token secret whose Secret has a readToken.
func (r *ProjectReconciler) packagesActionsOf(ctx context.Context, gp *c4v1beta1.GitProvider,
	provider gitprovider.Provider) (packagesActions, error) {
	want := packagesActions{variables: map[string]string{}, secrets: map[string]string{}}
	npmSecret, pypiSecret := packagesSecrets(gp)
	if packagesOf(gp) == (gitprovider.Packages{}) && npmSecret == "" && pypiSecret == "" {
		return want, nil
	}
	repo := provider.Repository("")
	want.variables[ActionsVarNpmScope] = repo.NpmScope
	want.variables[ActionsVarNpmRegistry] = repo.NpmRegistry
	want.variables[ActionsVarPyPIIndex] = repo.PyPIIndex
	for name, secret := range map[string]string{ActionsSecretNpmToken: npmSecret, ActionsSecretPyPIToken: pypiSecret} {
		token, err := r.readToken(ctx, gp.Namespace, secret)
		if err != nil {
			return want, err
		}
		if token != "" {
			want.secrets[name] = token
		}
	}
	return want, nil
}

// readToken is the readToken of the named Secret in ns, "" for no name, no
// Secret or no key.
func (r *ProjectReconciler) readToken(ctx context.Context, ns, name string) (string, error) {
	return secretKey(ctx, r.APIReader, ns, name, packagesReadTokenKey)
}

// applyPackagesActions sets want's variables and secrets on the owner and
// deletes the rest of the five.
func applyPackagesActions(ctx context.Context, actions gitprovider.OwnerActions, want packagesActions) error {
	for _, name := range []string{ActionsVarNpmScope, ActionsVarNpmRegistry, ActionsVarPyPIIndex} {
		var err error
		if value, ok := want.variables[name]; ok {
			err = actions.SetActionsVariable(ctx, name, value)
		} else {
			err = actions.DeleteActionsVariable(ctx, name)
		}
		if err != nil {
			return err
		}
	}
	for _, name := range []string{ActionsSecretNpmToken, ActionsSecretPyPIToken} {
		var err error
		if value, ok := want.secrets[name]; ok {
			err = actions.SetActionsSecret(ctx, name, value)
		} else {
			err = actions.DeleteActionsSecret(ctx, name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
