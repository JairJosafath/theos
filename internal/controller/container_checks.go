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
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// defaultContainerChecksRecheck is how often a Ready Container reads its checks
// again. The Git host doesn't call back, so this is polled.
const defaultContainerChecksRecheck = time.Minute

func (r *ContainerReconciler) checksRecheck() time.Duration {
	if r.ChecksRecheck > 0 {
		return r.ChecksRecheck
	}
	return defaultContainerChecksRecheck
}

// refreshChecks reads the latest CI result on the default branch into status.
// A Git error keeps the last known result: checks are a report, not a step.
func (r *ContainerReconciler) refreshChecks(ctx context.Context, container *c4v1alpha1.Container,
	status *c4v1alpha1.ContainerStatus) ctrl.Result {
	if !model.Protected(container.Spec.StandardRef != nil) {
		return ctrl.Result{}
	}
	provider, err := providerFor(ctx, r.Client, r.APIReader, r.Git, container.Namespace, container.Spec.GitProviderRef.Name)
	if err == nil {
		var checks gitprovider.CheckStatus
		gitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		checks, err = provider.CombinedStatus(gitCtx, provider.Repository(container.Name), gitprovider.DefaultBranch)
		cancel()
		if err == nil {
			status.Checks = &c4v1alpha1.CheckStatus{Commit: checks.Commit, State: checks.State, URL: checks.URL}
		}
	}
	if err != nil {
		logf.FromContext(ctx).Info("Could not read checks", "error", err.Error())
	}
	return ctrl.Result{RequeueAfter: r.checksRecheck()}
}

// providerFor returns the Provider for a GitProvider in namespace ns, with its
// credentials read straight from the API server.
func providerFor(ctx context.Context, c client.Client, apiReader client.Reader, git GitClient,
	ns, name string) (gitprovider.Provider, error) {
	access, err := gitAccessFor(ctx, c, apiReader, git, ns, name)
	return access.provider, err
}

// gitAccess is a GitProvider, its credentials and the Provider built from them.
type gitAccess struct {
	gitProvider *c4v1alpha1.GitProvider
	creds       gitprovider.Credentials
	provider    gitprovider.Provider
}

// gitAccessFor reads a GitProvider in namespace ns and its credentials, straight
// from the API server, and builds its Provider.
func gitAccessFor(ctx context.Context, c client.Client, apiReader client.Reader, git GitClient,
	ns, name string) (gitAccess, error) {
	var provider c4v1alpha1.GitProvider
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &provider); err != nil {
		return gitAccess{}, fmt.Errorf("GitProvider %s: %w", name, err)
	}
	creds, found, err := readCredentials(ctx, apiReader, &provider)
	if err != nil {
		return gitAccess{}, err
	}
	if !found {
		return gitAccess{}, fmt.Errorf("secret %s of GitProvider %s not found",
			provider.Spec.CredentialsSecretRef.Name, name)
	}
	p, err := git.Provider(gitprovider.Config{
		Kind:        string(provider.Spec.Provider),
		URL:         provider.Spec.URL,
		Owner:       provider.Spec.Owner,
		Credentials: creds,
	})
	if err != nil {
		return gitAccess{}, err
	}
	return gitAccess{gitProvider: &provider, creds: creds, provider: p}, nil
}
