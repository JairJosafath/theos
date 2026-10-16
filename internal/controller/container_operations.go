/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// operationsChoices is spec.operations as model reads it; nil leaves every
// field out.
func operationsChoices(spec *c4v1alpha1.OperationsSpec) model.OperationsChoices {
	var c model.OperationsChoices
	if spec == nil {
		return c
	}
	if b := spec.Backups; b != nil {
		c.RetentionDays, c.CopyToRegion = b.RetentionDays, b.CopyToRegion
	}
	if r := spec.Recovery; r != nil {
		c.RPO, c.RTO = r.RPO, r.RTO
	}
	if a := spec.Alerts; a != nil {
		c.Email = a.Email
	}
	return c
}

// plannedOperations is an existing Container's block as a plan's object
// carries it, nil when spec has none, so a plan applied again matches it.
func plannedOperations(spec *c4v1alpha1.OperationsSpec) *model.OperationsChoices {
	if spec == nil {
		return nil
	}
	c := operationsChoices(spec)
	return &c
}

// operationsSpec is a plan's block as spec.operations writes it: only the
// fields the plan set, so the factory's defaults stay its own; nil is nil.
func operationsSpec(c *model.OperationsChoices) *c4v1alpha1.OperationsSpec {
	if c == nil {
		return nil
	}
	spec := &c4v1alpha1.OperationsSpec{}
	if c.RetentionDays != nil || c.CopyToRegion != "" {
		spec.Backups = &c4v1alpha1.OperationsBackups{RetentionDays: c.RetentionDays, CopyToRegion: c.CopyToRegion}
	}
	if c.RPO != "" || c.RTO != "" {
		spec.Recovery = &c4v1alpha1.OperationsRecovery{RPO: c.RPO, RTO: c.RTO}
	}
	if c.Email != "" {
		spec.Alerts = &c4v1alpha1.OperationsAlerts{Email: c.Email}
	}
	return spec
}

// specOperations is the Container's operations block, the defaults filled in.
func specOperations(container *c4v1alpha1.Container) model.Operations {
	return model.ResolveOperations(operationsChoices(container.Spec.Operations))
}

// operationsStatus is a resolved block as status.operations records it, every
// field set.
func operationsStatus(o model.Operations) *c4v1alpha1.OperationsSpec {
	days := o.Backups.RetentionDays
	return &c4v1alpha1.OperationsSpec{
		Backups:  &c4v1alpha1.OperationsBackups{RetentionDays: &days, CopyToRegion: o.Backups.CopyToRegion},
		Recovery: &c4v1alpha1.OperationsRecovery{RPO: o.Recovery.RPO, RTO: o.Recovery.RTO},
		Alerts:   &c4v1alpha1.OperationsAlerts{Email: o.Alerts.Email},
	}
}

// scaffoldedOperations is the block the repository was rendered with, for a
// Standard update to render the same; a Container scaffolded before the
// factory kept it renders its block as it is now.
func scaffoldedOperations(container *c4v1alpha1.Container, status *c4v1alpha1.ContainerStatus) model.Operations {
	if status.Operations == nil {
		return specOperations(container)
	}
	return model.ResolveOperations(operationsChoices(status.Operations))
}

// reportOperationsApplied sets OperationsApplied on a scaffolded Container:
// True while spec.operations, the defaults filled in, is the block the
// scaffold rendered with, and False, ChangedAfterScaffold, once it differs.
// Nothing carries a change to the repository yet (plans/aws-production.md,
// step 12), so a Task changes infra/operations.ts like any other code. A
// Container scaffolded before the factory kept the block has the condition
// only once spec.operations says something.
func (r *ContainerReconciler) reportOperationsApplied(container *c4v1alpha1.Container,
	status *c4v1alpha1.ContainerStatus) {
	if status.Phase != c4v1alpha1.ContainerReady {
		meta.RemoveStatusCondition(&status.Conditions, c4v1alpha1.ConditionOperationsApplied)
		return
	}
	if status.Operations == nil {
		if operationsChoices(container.Spec.Operations) == (model.OperationsChoices{}) {
			meta.RemoveStatusCondition(&status.Conditions, c4v1alpha1.ConditionOperationsApplied)
			return
		}
		r.condition(container, status, c4v1alpha1.ConditionOperationsApplied, metav1.ConditionFalse,
			c4v1alpha1.ReasonChangedAfterScaffold, "The Container was scaffolded before the factory kept "+
				"spec.operations, so its repository has none of it; change infra/operations.ts by a Task")
		return
	}
	if specOperations(container) != scaffoldedOperations(container, status) {
		r.condition(container, status, c4v1alpha1.ConditionOperationsApplied, metav1.ConditionFalse,
			c4v1alpha1.ReasonChangedAfterScaffold, "spec.operations changed after the scaffold, and the factory "+
				"doesn't carry the change to the repository yet; change infra/operations.ts by a Task")
		return
	}
	r.condition(container, status, c4v1alpha1.ConditionOperationsApplied, metav1.ConditionTrue,
		c4v1alpha1.ReasonAppliedAtScaffold, "")
}
