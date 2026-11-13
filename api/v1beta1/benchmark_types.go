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

// BenchmarkGoal is one goal a Benchmark has each variant implement: a goal in
// plain words, or an issue of the Container's repository, read once when the
// Benchmark starts.
// +kubebuilder:validation:XValidation:rule="has(self.goal) != has(self.issue)",message="set exactly one of goal and issue"
type BenchmarkGoal struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=20000
	// +optional
	Goal string `json:"goal,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +optional
	Issue int32 `json:"issue,omitempty"`
}

// BenchmarkVariant is one configuration a Benchmark compares: the Agents that
// implement, review and verify its Tasks.
type BenchmarkVariant struct {
	// name names the variant in its Tasks' names and the status.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]{0,14}[a-z0-9])?$`
	// +required
	Name string `json:"name"`
	// implementerRef names the Agent that implements.
	// +required
	ImplementerRef LocalRef `json:"implementerRef"`
	// reviewerRef names the Agent that reviews; unset, the implementer.
	// +optional
	ReviewerRef *LocalRef `json:"reviewerRef,omitempty"`
	// verifierRef names the Agent that verifies an approved change; unset,
	// none.
	// +optional
	VerifierRef *LocalRef `json:"verifierRef,omitempty"`
}

// BenchmarkSpec runs the same goals, from one pinned commit, under two or
// more variants, each repeated, and compares what came out
// (docs/plans/done/measure-and-improve.md, step 6). Its Tasks never merge: one the
// reviewer approves with green checks ends Approved, and the Benchmark closes
// its pull request and deletes its branch once the Scorers it names judged it.
// It labels, closes and comments on no issue, and takes no Crew slot.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="a Benchmark can't change; make a new one"
type BenchmarkSpec struct {
	// containerRef names the Container whose repository the goals are built in.
	// +required
	ContainerRef LocalRef `json:"containerRef"`

	// base is the commit every Task branches from: a full commit, never a
	// branch, so each variant starts where the others did.
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{40}$`
	// +required
	Base string `json:"base"`

	// goals are what each variant implements.
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	// +required
	Goals []BenchmarkGoal `json:"goals"`

	// variants are the configurations compared, each named once.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=2
	// +kubebuilder:validation:MaxItems=4
	// +required
	Variants []BenchmarkVariant `json:"variants"`

	// repetitions is how many times each variant implements each goal.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=5
	// +kubebuilder:default=3
	// +optional
	Repetitions int32 `json:"repetitions,omitempty"`

	// scorerRefs name the Scorers that judge every one of its finished Tasks,
	// whatever their sample and selection.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=5
	// +optional
	ScorerRefs []LocalRef `json:"scorerRefs,omitempty"`

	// parallel is how many of its Tasks run at once.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=5
	// +kubebuilder:default=1
	// +optional
	Parallel int32 `json:"parallel,omitempty"`

	// budgetUSD caps what its Tasks' agent runs may cost, in US dollars; at the
	// cap it starts no further Task, reason OverBudget.
	// +kubebuilder:validation:Minimum=1
	// +required
	BudgetUSD int32 `json:"budgetUSD"`
}

// BenchmarkRate is a share with its 95% Wilson interval.
type BenchmarkRate struct {
	// of is how many it is a share of.
	// +optional
	Of int32 `json:"of,omitempty"`
	// percent, low and high are the share and its interval, in whole percent.
	// +optional
	Percent int32 `json:"percent,omitempty"`
	// +optional
	Low int32 `json:"low,omitempty"`
	// +optional
	High int32 `json:"high,omitempty"`
}

// BenchmarkCriterionRate is a Scorer criterion's pass rate in a variant.
type BenchmarkCriterionRate struct {
	// scorer and criterion name it.
	// +kubebuilder:validation:MaxLength=253
	// +required
	Scorer string `json:"scorer"`
	// +kubebuilder:validation:MaxLength=500
	// +required
	Criterion string `json:"criterion"`
	// rate is pass out of pass and fail; unclear counts the rest.
	// +optional
	Rate BenchmarkRate `json:"rate,omitzero"`
	// +optional
	Unclear int32 `json:"unclear,omitempty"`
}

// BenchmarkVariantStatus is what one variant's Tasks came to.
type BenchmarkVariantStatus struct {
	// +required
	Name string `json:"name"`
	// tasks, finished and approved count its Tasks.
	// +optional
	Tasks int32 `json:"tasks,omitempty"`
	// +optional
	Finished int32 `json:"finished,omitempty"`
	// approval is approved out of finished.
	// +optional
	Approval BenchmarkRate `json:"approval,omitzero"`
	// meanRounds, medianMinutes and medianCostMilliUSD are over its finished
	// Tasks.
	// +kubebuilder:validation:MaxLength=16
	// +optional
	MeanRounds string `json:"meanRounds,omitempty"`
	// +optional
	MedianMinutes int32 `json:"medianMinutes,omitempty"`
	// +optional
	MedianCostMilliUSD int64 `json:"medianCostMilliUSD,omitempty"`
	// criteria are its Scorers' pass rates.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Criteria []BenchmarkCriterionRate `json:"criteria,omitempty"`
}

// BenchmarkStatus is how the Benchmark goes and what it found.
type BenchmarkStatus struct {
	// goals are the goals as the Tasks got them, an issue's read once.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=10
	// +optional
	Goals []string `json:"goals,omitempty"`

	// costMilliUSD is what its Tasks cost so far, in thousandths of a US dollar.
	// +optional
	CostMilliUSD int64 `json:"costMilliUSD,omitempty"`

	// variants are each variant's results.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=4
	// +optional
	Variants []BenchmarkVariantStatus `json:"variants,omitempty"`

	// comparison says in a sentence what the results show: a variant ahead
	// only when the intervals don't overlap, else no clear difference.
	// +kubebuilder:validation:MaxLength=1000
	// +optional
	Comparison string `json:"comparison,omitempty"`

	// cleaned names its finished Tasks whose pull request it closed and whose
	// branch it deleted.
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=200
	// +optional
	Cleaned []string `json:"cleaned,omitempty"`

	// conditions: Done, False while Tasks run (reason Running) or when it stops
	// starting them (OverBudget, ContainerNotFound, IssueUnreadable), True
	// once every Task finished.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ConditionBenchmarkDone is a Benchmark's condition: True once every Task
// finished and was cleaned up.
const ConditionBenchmarkDone = "Done"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,categories=theos
// +kubebuilder:printcolumn:name="Container",type=string,JSONPath=".spec.containerRef.name"
// +kubebuilder:printcolumn:name="Done",type=string,JSONPath=".status.conditions[?(@.type==\"Done\")].status"
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=".status.conditions[?(@.type==\"Done\")].reason"
// +kubebuilder:printcolumn:name="Comparison",type=string,JSONPath=".status.comparison",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Benchmark compares Agent configurations on the same goals
// (docs/plans/done/measure-and-improve.md).
type Benchmark struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec is the goals, the base and the variants.
	// +required
	Spec BenchmarkSpec `json:"spec"`

	// status is how it goes and what it found.
	// +optional
	Status BenchmarkStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// BenchmarkList contains a list of Benchmark
type BenchmarkList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Benchmark `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Benchmark{}, &BenchmarkList{})
		return nil
	})
}
