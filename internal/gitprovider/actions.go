package gitprovider

import "context"

// OwnerActions is an optional capability of the CI port: the Actions
// variables and secrets of the provider's owner organization, which every
// workflow of the owner's repositories reads. The operator sets the package
// registries there when a GitProvider's spec.packages departs from the
// host's own (docs/plans/app-packages.md, decision 5). A Provider that has
// it implements it too; find it with a type assertion.
type OwnerActions interface {
	// SetActionsVariable creates or updates the owner organization's
	// variable name.
	SetActionsVariable(ctx context.Context, name, value string) error
	// DeleteActionsVariable removes the owner organization's variable name.
	// It is safe to repeat: one that is gone already is no error.
	DeleteActionsVariable(ctx context.Context, name string) error
	// SetActionsSecret creates or updates the owner organization's secret
	// name.
	SetActionsSecret(ctx context.Context, name, value string) error
	// DeleteActionsSecret removes the owner organization's secret name. It
	// is safe to repeat: one that is gone already is no error.
	DeleteActionsSecret(ctx context.Context, name string) error
}
