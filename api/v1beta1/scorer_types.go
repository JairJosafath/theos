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

// ScorerSelect is which finished Tasks a Scorer judges.
type ScorerSelect struct {
	// kinds are the Task kinds it judges; empty means Implement. A Plan Task
	// has no Container to judge, and a Score Task is never judged.
	// +listType=set
	// +kubebuilder:validation:MaxItems=3
	// +kubebuilder:validation:XValidation:rule="self.all(k, k in ['Implement', 'Spec', 'Triage'])",message="a Scorer judges Implement, Spec and Triage Tasks"
	// +optional
	Kinds []TaskKind `json:"kinds,omitempty"`

	// containers names the Containers whose Tasks it judges; empty means every
	// Container of the namespace.
	// +listType=set
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=63
	// +optional
	Containers []string `json:"containers,omitempty"`

	// phases are the outcomes it judges; empty means Merged.
	// +listType=set
	// +kubebuilder:validation:MaxItems=2
	// +kubebuilder:validation:XValidation:rule="self.all(p, p in ['Merged', 'Failed'])",message="a Scorer judges Merged and Failed Tasks"
	// +optional
	Phases []TaskPhase `json:"phases,omitempty"`
}

// ScorerSpec is plain-words criteria a judge Agent checks finished Tasks
// against (docs/plans/done/measure-and-improve.md). For each Task it selects and
// samples that finishes after the Scorer was made, the operator makes a Task
// of kind Score: one read-only run of the judge on the finished change, whose
// verdicts, one per criterion, are its status.score. Scores advise: nothing
// about the judged Task changes.
type ScorerSpec struct {
	// criteria are what the judge checks, each in plain words, such as "The
	// change has a test that fails without it". A change makes a new
	// generation: the Scorer's summary counts only the current one's Scores.
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=500
	// +required
	Criteria []string `json:"criteria"`

	// judgeRef names the Agent that judges: its harness, model, image and
	// limits, as any Agent's.
	// +required
	JudgeRef LocalRef `json:"judgeRef"`

	// select says which finished Tasks it judges; empty means every merged
	// Implement Task of the namespace.
	// +optional
	Select ScorerSelect `json:"select,omitzero"`

	// samplePercent is how many of the selected Tasks it judges, picked by a
	// hash of the Scorer's and the Task's names, so the same Tasks every
	// time. 0 stops it.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +kubebuilder:default=100
	// +optional
	SamplePercent *int32 `json:"samplePercent,omitempty"`

	// maxPerDay caps the judge runs it starts in a UTC day.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	// +kubebuilder:default=50
	// +optional
	MaxPerDay int32 `json:"maxPerDay,omitempty"`

	// budgetUSD caps what its judge runs may cost in a UTC day, in US dollars,
	// summed from what the agents report; at the cap it starts no run until
	// the next day, reason BudgetReached. Unset means no cap.
	// +kubebuilder:validation:Minimum=1
	// +optional
	BudgetUSD int32 `json:"budgetUSD,omitempty"`

	// improve, when set, has a criterion that keeps failing for one
	// configuration file an issue that proposes the fix: status/proposed,
	// kind/improvement, in the repository of the newest failing Task's
	// Container, and no other while it is open. Triage and the loop decide;
	// nothing changes until a change merges. Unset: it files nothing.
	// +optional
	Improve *ScorerImprove `json:"improve,omitempty"`
}

// ScorerImprove is when a Scorer proposes a change.
// +kubebuilder:validation:XValidation:rule="self.failures <= self.window",message="failures can't exceed window"
type ScorerImprove struct {
	// failures is how many of the newest window Scores of one configuration
	// must fail a criterion.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=50
	// +kubebuilder:default=3
	// +optional
	Failures int32 `json:"failures,omitempty"`

	// window is how many of a configuration's newest finished Scores it looks
	// at.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=50
	// +kubebuilder:default=10
	// +optional
	Window int32 `json:"window,omitempty"`
}

// ScorerCriterionCount is how the current generation's Scores judged one
// criterion.
type ScorerCriterionCount struct {
	// criterion is the criterion's text.
	// +kubebuilder:validation:MaxLength=500
	// +required
	Criterion string `json:"criterion"`

	// pass, fail and unclear count its verdicts.
	// +optional
	Pass int32 `json:"pass,omitempty"`
	// +optional
	Fail int32 `json:"fail,omitempty"`
	// +optional
	Unclear int32 `json:"unclear,omitempty"`
}

// ScorerStatus is what the Scorer has judged.
type ScorerStatus struct {
	// scored counts the current generation's finished Scores.
	// +optional
	Scored int32 `json:"scored,omitempty"`

	// criteria counts each current criterion's verdicts, in the spec's order.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=20
	// +optional
	Criteria []ScorerCriterionCount `json:"criteria,omitempty"`

	// startedToday counts the judge runs it started in the UTC day of day,
	// and costTodayMilliUSD what they cost so far, in thousandths of a US
	// dollar.
	// +optional
	StartedToday int32 `json:"startedToday,omitempty"`
	// +optional
	CostTodayMilliUSD int64 `json:"costTodayMilliUSD,omitempty"`
	// day is the UTC day of startedToday, YYYY-MM-DD.
	// +kubebuilder:validation:MaxLength=10
	// +optional
	Day string `json:"day,omitempty"`

	// observedGeneration is the generation the status is of.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions: Ready, False with reason JudgeNotFound, Paused (a
	// samplePercent of 0), DailyCapReached or BudgetReached.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ConditionScorerReady is a Scorer's condition: True while it judges the Tasks
// it selects.
const ConditionScorerReady = "Ready"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=theos
// +kubebuilder:printcolumn:name="Judge",type=string,JSONPath=".spec.judgeRef.name"
// +kubebuilder:printcolumn:name="Scored",type=integer,JSONPath=".status.scored"
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=".status.conditions[?(@.type==\"Ready\")].reason"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Scorer judges finished Tasks against plain-words criteria
// (docs/plans/done/measure-and-improve.md).
type Scorer struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec is the criteria, the judge and what it judges.
	// +required
	Spec ScorerSpec `json:"spec"`

	// status is what it judged.
	// +optional
	Status ScorerStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ScorerList contains a list of Scorer
type ScorerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Scorer `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Scorer{}, &ScorerList{})
		return nil
	})
}
