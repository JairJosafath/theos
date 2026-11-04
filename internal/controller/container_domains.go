package controller

import (
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/model"
)

// scaffoldedDomains are the Domains the scaffold wrote, as it passed them to
// the templates: status.scaffoldedDomains, sorted and unique. They are
// recorded, never inferred from scaffoldedFrom: a changed choice or a Domain
// sharing a name with a Connector would otherwise be misread (#1757). An
// adopted Container (nothing recorded as scaffolded) has none.
func scaffoldedDomains(status *c4v1beta1.ContainerStatus) []string {
	names := slices.Clone(status.ScaffoldedDomains)
	slices.Sort(names)
	return slices.Compact(names)
}

// reportDomainsApplied sets DomainsApplied on a Ready Container: True when its
// system names the Domains it was scaffolded with, False with
// AddedAfterScaffold when they differ (a Domain is written once, at the
// scaffold), absent when the system names none and none was scaffolded.
func reportDomainsApplied(container *c4v1beta1.Container, status *c4v1beta1.ContainerStatus,
	system *c4v1beta1.SoftwareSystem) {
	have, wanted := scaffoldedDomains(status), domainNames(system)
	if len(have) == 0 && len(wanted) == 0 {
		meta.RemoveStatusCondition(&status.Conditions, c4v1beta1.ConditionDomainsApplied)
		return
	}
	added, removed := model.DomainsDiff(have, wanted)
	if len(added) == 0 && len(removed) == 0 {
		setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionDomainsApplied,
			metav1.ConditionTrue, c4v1beta1.ReasonAppliedAtScaffold, "")
		return
	}
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "added after the scaffold: "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed since the scaffold: "+strings.Join(removed, ", "))
	}
	setCondition(&status.Conditions, container.Generation, c4v1beta1.ConditionDomainsApplied,
		metav1.ConditionFalse, c4v1beta1.ReasonAddedAfterScaffold,
		fmt.Sprintf("Domains %s; scaffolding happens once", strings.Join(parts, "; ")))
}
