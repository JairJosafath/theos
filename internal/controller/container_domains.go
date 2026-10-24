package controller

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/model"
)

// scaffoldedDomains are the Domains the scaffold wrote, as it passed them to
// the templates: sorted and unique, read from status.scaffoldedFrom as the
// entries after the Container's sides and Connectors. An adopted Container
// (nothing recorded as scaffolded) has none.
func scaffoldedDomains(container *c4v1alpha1.Container, status *c4v1alpha1.ContainerStatus) []string {
	choices := container.Choices()
	others := append([]string{choices.Framework, choices.Standard, choices.Compute, choices.Deployment},
		scaffoldedConnectors(status)...)
	return model.ScaffoldedDomains(status.ScaffoldedFrom, others)
}

// reportDomainsApplied sets DomainsApplied on a Ready Container: True when its
// system names the Domains it was scaffolded with, False with
// AddedAfterScaffold when they differ (a Domain is written once, at the
// scaffold), absent when the system names none and none was scaffolded.
func reportDomainsApplied(container *c4v1alpha1.Container, status *c4v1alpha1.ContainerStatus,
	system *c4v1alpha1.SoftwareSystem) {
	have, wanted := scaffoldedDomains(container, status), domainNames(system)
	if len(have) == 0 && len(wanted) == 0 {
		meta.RemoveStatusCondition(&status.Conditions, c4v1alpha1.ConditionDomainsApplied)
		return
	}
	added, removed := model.DomainsDiff(have, wanted)
	if len(added) == 0 && len(removed) == 0 {
		setCondition(&status.Conditions, container.Generation, c4v1alpha1.ConditionDomainsApplied,
			metav1.ConditionTrue, c4v1alpha1.ReasonAppliedAtScaffold, "")
		return
	}
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "added after the scaffold: "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed since the scaffold: "+strings.Join(removed, ", "))
	}
	setCondition(&status.Conditions, container.Generation, c4v1alpha1.ConditionDomainsApplied,
		metav1.ConditionFalse, c4v1alpha1.ReasonAddedAfterScaffold,
		fmt.Sprintf("Domains %s; scaffolding happens once", strings.Join(parts, "; ")))
}
