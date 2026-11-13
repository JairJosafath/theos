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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AlertSourceType is whose deliveries an AlertSource takes; it decides what
// makes two deliveries the same alert (docs/plans/done/alerts.md, decisions 5 to 7).
// +kubebuilder:validation:Enum=alertmanager;sentry;generic
type AlertSourceType string

// AlertSource types.
const (
	AlertSourceAlertmanager AlertSourceType = "alertmanager"
	AlertSourceSentry       AlertSourceType = "sentry"
	AlertSourceGeneric      AlertSourceType = "generic"
)

// AlertSourceSpec names one sender of alerts and how its alerts are read
// (docs/plans/done/alerts.md, decision 1). The label rule below is #187.
// +kubebuilder:validation:XValidation:rule="(has(self.containerLabel) ? self.containerLabel : 'theos_container') != (has(self.namespaceLabel) ? self.namespaceLabel : 'namespace')",message="containerLabel and namespaceLabel must differ, their defaults theos_container and namespace included: one label can't name both an alert's Container and its namespace"
type AlertSourceSpec struct {
	// type is the sender: alertmanager, sentry or generic. Immutable, since
	// the type decides what makes two deliveries the same alert.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="type is immutable: it decides what makes two deliveries the same alert; make a new AlertSource"
	// +required
	Type AlertSourceType `json:"type"`

	// secretRef names a Secret in the namespace that checks each delivery:
	// key "token" for alertmanager (a bearer token), "secret" for sentry
	// (the integration's client secret), and for generic "secret" (an
	// HMAC key) or else "token".
	// +required
	SecretRef LocalRef `json:"secretRef"`

	// containerLabel is the label, tag or field of an alert that names its
	// Container.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Pattern=`^[^\p{Z}\p{C}]+$`
	// +kubebuilder:default=theos_container
	// +optional
	ContainerLabel string `json:"containerLabel,omitempty"`

	// namespaceLabel is the label of an Alertmanager alert, or of a generic
	// body that has no namespace field, that names the namespace its app
	// runs in: namespace as Prometheus's Kubernetes metrics carry it, or the
	// name a Prometheus that relabels gives it (exported_namespace,
	// kubernetes_namespace).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Pattern=`^[^\p{Z}\p{C}]+$`
	// +kubebuilder:default=namespace
	// +optional
	NamespaceLabel string `json:"namespaceLabel,omitempty"`

	// routes say where alerts go that name no Container: the first whose
	// namespace or project is the alert's.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=32
	// +optional
	Routes []AlertSourceRoute `json:"routes,omitempty"`

	// commentInterval is the least time between two comments about one
	// alert, and between two "and N more"; at least 1m.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:XValidation:rule="duration(self) >= duration('1m')",message="commentInterval must be a duration of at least 1m"
	// +kubebuilder:default="1h"
	// +optional
	CommentInterval *metav1.Duration `json:"commentInterval,omitempty"`

	// dailyLimit is how many issues the source files for one Container in
	// one UTC day; past it, the day's alerts fold into "and N more".
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=5
	// +optional
	DailyLimit int32 `json:"dailyLimit,omitempty"`
}

// AlertSourceRoute sends the alerts of one namespace, or of one project of
// the sender, that name no Container to a Container.
// +kubebuilder:validation:XValidation:rule="has(self.__namespace__) != has(self.project)",message="a route has exactly one of namespace and project"
type AlertSourceRoute struct {
	// container is the Container of the AlertSource's namespace the alerts
	// go to.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	// +required
	Container string `json:"container"`

	// namespace is the namespace an alert's app runs in, as the label
	// namespaceLabel names carries it.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// project is the sender's project, such as Sentry's project slug.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Pattern=`^[^\p{Z}\p{C}]+$`
	// +optional
	Project string `json:"project,omitempty"`
}

// ConditionAlertSourceReady on an AlertSource says whether it takes
// deliveries. Its reasons are Receiving (True), SecretMissing (no Secret,
// none of the keys its type reads, or an empty value under the key) and
// ReceiverOff (the operator's listener is off).
const ConditionAlertSourceReady = "Ready"

// The reasons of an AlertSource's Ready condition.
const (
	ReasonAlertSourceReceiving     = "Receiving"
	ReasonAlertSourceSecretMissing = "SecretMissing"
	ReasonAlertSourceReceiverOff   = "ReceiverOff"
)

// AlertSourceStatus is what the operator took from the sender and filed.
// Written by the AlertSource's reconciler alone; it holds no text of an
// alert but its name.
type AlertSourceStatus struct {
	// conditions: Ready, whether the source takes deliveries.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// url is where the sender delivers: the path, after the operator's
	// --git-events-url when that is set.
	// +optional
	URL string `json:"url,omitempty"`

	// deliveries counts the deliveries taken.
	// +optional
	Deliveries int64 `json:"deliveries,omitempty"`

	// lastDeliveryTime is when the last delivery was taken.
	// +optional
	LastDeliveryTime *metav1.Time `json:"lastDeliveryTime,omitempty"`

	// unmatched counts the alerts that named no known Container, and says
	// why the last one didn't.
	// +optional
	Unmatched *AlertSourceUnmatched `json:"unmatched,omitempty"`

	// unread counts the alerts that deliveries held past the first 100, or
	// that their sender said it left out.
	// +optional
	Unread *AlertSourceUnread `json:"unread,omitempty"`

	// alerts are the alerts the source remembers, at most 100.
	// +listType=map
	// +listMapKey=key
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Alerts []AlertSourceAlert `json:"alerts,omitempty"`

	// containers hold each Container's day: what it filed and folded.
	// +listType=map
	// +listMapKey=name
	// +optional
	Containers []AlertSourceContainerDay `json:"containers,omitempty"`
}

// AlertSourceUnmatched counts the alerts that named no known Container.
type AlertSourceUnmatched struct {
	// count is how many.
	// +optional
	Count int64 `json:"count,omitempty"`

	// lastTime is when the last came.
	// +optional
	LastTime *metav1.Time `json:"lastTime,omitempty"`

	// message says why the last one found no Container.
	// +kubebuilder:validation:MaxLength=1024
	// +optional
	Message string `json:"message,omitempty"`
}

// AlertSourceUnread counts the alerts no delivery let the source read.
type AlertSourceUnread struct {
	// count is how many.
	// +optional
	Count int64 `json:"count,omitempty"`

	// lastTime is when the last came.
	// +optional
	LastTime *metav1.Time `json:"lastTime,omitempty"`
}

// AlertSourceAlert is what the source remembers of one alert: model.AlertRecord
// field for field.
type AlertSourceAlert struct {
	// key is what makes two deliveries the same alert.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +required
	Key string `json:"key"`

	// container is the Container the alert belongs to.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	Container string `json:"container,omitempty"`

	// name is the alert's, for people who read status.
	// +kubebuilder:validation:MaxLength=120
	// +optional
	Name string `json:"name,omitempty"`

	// issue is the number of the issue the alert was filed as, or the one
	// its day's "and N more" is said on when it was folded.
	// +optional
	Issue int32 `json:"issue,omitempty"`

	// folded says the alert came past the day's limit and filed nothing.
	// +optional
	Folded bool `json:"folded,omitempty"`

	// resolved says the last delivery reported the alert over.
	// +optional
	Resolved bool `json:"resolved,omitempty"`

	// filedAt is when it was filed or folded.
	// +optional
	FiledAt *metav1.Time `json:"filedAt,omitempty"`

	// lastSeen is its last delivery.
	// +optional
	LastSeen *metav1.Time `json:"lastSeen,omitempty"`

	// count is its firing deliveries since filedAt, the first included.
	// +optional
	Count int64 `json:"count,omitempty"`

	// reported is the count its issue has said.
	// +optional
	Reported int64 `json:"reported,omitempty"`

	// reportedAt is when its issue last said anything of it.
	// +optional
	ReportedAt *metav1.Time `json:"reportedAt,omitempty"`

	// saidResolved says that what its issue last said was that it resolved.
	// +optional
	SaidResolved bool `json:"saidResolved,omitempty"`
}

// AlertSourceContainerDay is what the source keeps of one Container's UTC
// day: model.AlertDay field for field.
type AlertSourceContainerDay struct {
	// name is the Container's.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +required
	Name string `json:"name"`

	// date is the UTC day, 2006-01-02.
	// +kubebuilder:validation:Pattern=`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`
	// +optional
	Date string `json:"date,omitempty"`

	// filed counts the issues filed that day.
	// +optional
	Filed int32 `json:"filed,omitempty"`

	// lastIssue is the last issue filed that day, where its "and N more"
	// is said.
	// +optional
	LastIssue int32 `json:"lastIssue,omitempty"`

	// folded counts the alerts past the limit that day, each once.
	// +optional
	Folded int64 `json:"folded,omitempty"`

	// unrecorded counts the deliveries of alerts past the limit that status
	// had no room to remember.
	// +optional
	Unrecorded int64 `json:"unrecorded,omitempty"`

	// foldReported is folded and unrecorded together as the last "and N
	// more" said them.
	// +optional
	FoldReported int64 `json:"foldReported,omitempty"`

	// foldReportedAt is when the last "and N more" was said.
	// +optional
	FoldReportedAt *metav1.Time `json:"foldReportedAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=theos
// +kubebuilder:storageversion
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].reason"
// +kubebuilder:printcolumn:name="Deliveries",type=integer,JSONPath=".status.deliveries",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// AlertSource names one sender of alerts, as a GitProvider names one Git
// host: an Alertmanager, a Sentry or any tool that posts JSON. Its firing
// alerts become bugs in the repositories of the Containers they concern
// (docs/plans/done/alerts.md). It belongs to the namespace, with no parent, and
// any number of Containers share it.
type AlertSource struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of AlertSource
	// +required
	Spec AlertSourceSpec `json:"spec"`

	// status defines the observed state of AlertSource
	// +optional
	Status AlertSourceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AlertSourceList contains a list of AlertSource
type AlertSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AlertSource `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &AlertSource{}, &AlertSourceList{})
		return nil
	})
}
