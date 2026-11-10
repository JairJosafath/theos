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
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// operationsChoices is spec.operations as model reads it; nil leaves every
// field out.
func operationsChoices(spec *c4v1beta1.OperationsSpec) model.OperationsChoices {
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
func plannedOperations(spec *c4v1beta1.OperationsSpec) *model.OperationsChoices {
	if spec == nil {
		return nil
	}
	c := operationsChoices(spec)
	return &c
}

// operationsSpec is a plan's block as spec.operations writes it: only the
// fields the plan set, so the factory's defaults stay its own; nil is nil.
func operationsSpec(c *model.OperationsChoices) *c4v1beta1.OperationsSpec {
	if c == nil {
		return nil
	}
	spec := &c4v1beta1.OperationsSpec{}
	if c.RetentionDays != nil || c.CopyToRegion != "" {
		spec.Backups = &c4v1beta1.OperationsBackups{RetentionDays: c.RetentionDays, CopyToRegion: c.CopyToRegion}
	}
	if c.RPO != "" || c.RTO != "" {
		spec.Recovery = &c4v1beta1.OperationsRecovery{RPO: c.RPO, RTO: c.RTO}
	}
	if c.Email != "" {
		spec.Alerts = &c4v1beta1.OperationsAlerts{Email: c.Email}
	}
	return spec
}

// specOperations is the Container's operations block, the defaults filled in.
func specOperations(container *c4v1beta1.Container) model.Operations {
	return model.ResolveOperations(operationsChoices(container.Spec.Operations))
}

// operationsStatus is a resolved block as status.operations records it, every
// field set.
func operationsStatus(o model.Operations) *c4v1beta1.OperationsSpec {
	days := o.Backups.RetentionDays
	return &c4v1beta1.OperationsSpec{
		Backups:  &c4v1beta1.OperationsBackups{RetentionDays: &days, CopyToRegion: o.Backups.CopyToRegion},
		Recovery: &c4v1beta1.OperationsRecovery{RPO: o.Recovery.RPO, RTO: o.Recovery.RTO},
		Alerts:   &c4v1beta1.OperationsAlerts{Email: o.Alerts.Email},
	}
}

// scaffoldedOperations is the block the repository was last rendered with, at
// the scaffold or by an operations update, for a Standard update to render the
// same; a Container scaffolded before the
// factory kept it renders its block as it is now.
func scaffoldedOperations(container *c4v1beta1.Container, status *c4v1beta1.ContainerStatus) model.Operations {
	if status.Operations == nil {
		return specOperations(container)
	}
	return model.ResolveOperations(operationsChoices(status.Operations))
}

// reportOperationsApplied sets OperationsApplied where no operations update
// acts, and reports false where one does: spec.operations, the defaults
// filled in, changed from the block the repository was last rendered with,
// or an update hasn't ended. A Container that isn't scaffolded has no
// condition. One whose Framework doesn't read the block (status.operations
// is nil: readsOperations isn't set on the Framework, or the Container was
// scaffolded before the factory kept the block) has it only once
// spec.operations says something, and then False, NotReadByFramework: its
// repository has none of the block, so a Task changes it (#1209).
func (r *ContainerReconciler) reportOperationsApplied(container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus) bool {
	if status.Phase != c4v1beta1.ContainerReady {
		meta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionOperationsApplied)
		return true
	}
	if status.Operations == nil {
		if operationsChoices(container.Spec.Operations) == (model.OperationsChoices{}) {
			meta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionOperationsApplied)
			return true
		}
		message := fmt.Sprintf("Framework %s doesn't read spec.operations (readsOperations is not set), so its "+
			"repository has none of it; change the repository by a Task", container.Choices().Framework)
		if container.Choices().Framework == "" {
			// An adopted repository with no Framework (#60).
			message = "The Container names no Framework to read spec.operations, so its repository has none of " +
				"it; change the repository by a Task"
		}
		r.condition(container, status, c4v1beta1.ConditionOperationsApplied, metav1.ConditionFalse,
			c4v1beta1.ReasonNotReadByFramework, message)
		return true
	}
	if model.NextOperationsStep(operationsFacts(container, status)).Action != model.OperationsUpToDate {
		return false
	}
	r.operationsUpToDate(container, status)
	return true
}

// operationsUpToDate reports the repository's infra/operations.ts rendered
// from spec.operations: at the scaffold, by the update that merged, or found
// so on main.
func (r *ContainerReconciler) operationsUpToDate(container *c4v1beta1.Container,
	status *c4v1beta1.ContainerStatus) {
	reason, message := c4v1beta1.ReasonAppliedAtScaffold, ""
	switch u := status.OperationsUpdate; {
	case u != nil && u.MergeCommit != "" && u.PullRequestURL != "":
		reason, message = c4v1beta1.ReasonUpdateMerged, fmt.Sprintf(
			"Pull request %s merged spec.operations into %s as %s", u.PullRequestURL, model.OperationsFile,
			u.MergeCommit)
	case u != nil && u.PullRequestURL != "":
		// The host named no commit for the merge, and main couldn't be read.
		reason, message = c4v1beta1.ReasonUpdateMerged, fmt.Sprintf(
			"Pull request %s merged spec.operations into %s", u.PullRequestURL, model.OperationsFile)
	case u != nil && u.MergeCommit != "":
		reason, message = c4v1beta1.ReasonUpToDate, fmt.Sprintf("%s at %s already renders spec.operations",
			model.OperationsFile, u.MergeCommit)
	}
	r.condition(container, status, c4v1beta1.ConditionOperationsApplied, metav1.ConditionTrue, reason, message)
}
