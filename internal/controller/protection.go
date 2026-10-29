package controller

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1beta1 "github.com/JairJosafath/c4factory/api/v1beta1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
	"github.com/JairJosafath/c4factory/internal/ports"
)

// gitProviderCI returns the CI system of a GitProvider's Git host: the host's
// own, since no GitProvider names another yet.
func gitProviderCI(gp *c4v1beta1.GitProvider) (model.CI, error) {
	ci, err := model.HostCI(string(gp.Spec.Provider))
	if err != nil {
		return model.CI{}, fmt.Errorf("GitProvider %s: %w", gp.Name, err)
	}
	return ci, nil
}

// containerCI returns the CI system the Container's repository runs.
func containerCI(ctx context.Context, c client.Reader, container *c4v1beta1.Container) (model.CI, error) {
	var gp c4v1beta1.GitProvider
	key := client.ObjectKey{Namespace: container.Namespace, Name: container.Spec.GitProviderRef.Name}
	if err := c.Get(ctx, key, &gp); err != nil {
		return model.CI{}, fmt.Errorf("GitProvider %s: %w", key.Name, err)
	}
	return gitProviderCI(&gp)
}

// protectedPaths returns the paths agents may not change in the Container: the
// platform's for its CI system and its Standard's.
func protectedPaths(ctx context.Context, c client.Reader, container *c4v1beta1.Container) ([]string, error) {
	_, paths, err := protection(ctx, c, container)
	return paths, err
}

// protection returns the Container's CI system and the paths agents may not
// change in it.
func protection(ctx context.Context, c client.Reader, container *c4v1beta1.Container) (model.CI, []string, error) {
	ci, err := containerCI(ctx, c, container)
	if err != nil {
		return model.CI{}, nil, err
	}
	var standard []string
	if name := container.Choices().Standard; name != "" {
		var s c4v1beta1.Standard
		if err := c.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: name}, &s); err != nil {
			return model.CI{}, nil, fmt.Errorf("reading Standard %s: %w", name, err)
		}
		standard = s.Spec.ProtectedPaths
	}
	return ci, model.AgentProtectedPaths(ci, standard), nil
}

// protectRepository makes the Container's default branch take changes only
// through pull requests that pass its CI system's checks, leave the protected paths alone and
// are merged by the operator, and keeps release tags to the operator. It does
// nothing for a Container without a Standard, which has no checks to require.
// It is safe to repeat, so it also brings older repositories up to date.
func protectRepository(ctx context.Context, c client.Reader, provider ports.GitHost,
	repo gitprovider.Repository, container *c4v1beta1.Container) error {
	if !model.Protected(container.Choices().Standard != "") {
		return nil
	}
	ci, paths, err := protection(ctx, c, container)
	if err != nil {
		return err
	}
	rule := gitprovider.BranchRule{StatusContexts: ci.CheckContexts, ProtectedPaths: paths}
	if err := provider.ProtectBranch(ctx, repo, gitprovider.DefaultBranch, rule); err != nil {
		return fmt.Errorf("protecting %s: %w", gitprovider.DefaultBranch, err)
	}
	if err := provider.ProtectTags(ctx, repo, model.ReleaseTags); err != nil {
		return fmt.Errorf("protecting tags %s: %w", model.ReleaseTags, err)
	}
	return nil
}
