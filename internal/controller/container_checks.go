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
	"fmt"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// checksRecheck is how often a Ready Container reads its checks again. The Git
// host doesn't call back, so this is polled.
const checksRecheck = time.Minute

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
	return ctrl.Result{RequeueAfter: checksRecheck}
}

// providerFor returns the Provider for a GitProvider in namespace ns, with its
// credentials read straight from the API server.
func providerFor(ctx context.Context, c client.Client, apiReader client.Reader, git GitClient,
	ns, name string) (gitprovider.Provider, error) {
	var provider c4v1alpha1.GitProvider
	if err := c.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, &provider); err != nil {
		return nil, fmt.Errorf("GitProvider %s: %w", name, err)
	}
	creds, found, err := readCredentials(ctx, apiReader, &provider)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("secret %s of GitProvider %s not found", provider.Spec.CredentialsSecretRef.Name, name)
	}
	return git.Provider(gitprovider.Config{
		Kind:        string(provider.Spec.Provider),
		URL:         provider.Spec.URL,
		Owner:       provider.Spec.Owner,
		Credentials: creds,
	})
}
