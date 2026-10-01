package controller

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
	"github.com/JairJosafath/c4factory/internal/model"
)

// protectedPaths returns the paths agents may not change in the Container: the
// platform's and its Standard's.
func protectedPaths(ctx context.Context, c client.Reader, container *c4v1alpha1.Container) ([]string, error) {
	var standard []string
	if ref := container.Spec.StandardRef; ref != nil {
		var s c4v1alpha1.Standard
		if err := c.Get(ctx, client.ObjectKey{Namespace: container.Namespace, Name: ref.Name}, &s); err != nil {
			return nil, fmt.Errorf("reading Standard %s: %w", ref.Name, err)
		}
		standard = s.Spec.ProtectedPaths
	}
	return model.AgentProtectedPaths(standard), nil
}

// protectRepository makes the Container's default branch take changes only
// through pull requests that pass its checks, leave the protected paths alone and
// are merged by the operator, and keeps release tags to the operator. It does
// nothing for a Container without a Standard, which has no checks to require.
// It is safe to repeat, so it also brings older repositories up to date.
func protectRepository(ctx context.Context, c client.Reader, provider gitprovider.Provider,
	repo gitprovider.Repository, container *c4v1alpha1.Container) error {
	if !model.Protected(container.Spec.StandardRef != nil) {
		return nil
	}
	paths, err := protectedPaths(ctx, c, container)
	if err != nil {
		return err
	}
	rule := gitprovider.BranchRule{StatusContexts: model.CheckContexts, ProtectedPaths: paths}
	if err := provider.ProtectBranch(ctx, repo, gitprovider.DefaultBranch, rule); err != nil {
		return fmt.Errorf("protecting %s: %w", gitprovider.DefaultBranch, err)
	}
	if err := provider.ProtectTags(ctx, repo, model.ReleaseTags); err != nil {
		return fmt.Errorf("protecting tags %s: %w", model.ReleaseTags, err)
	}
	return nil
}
