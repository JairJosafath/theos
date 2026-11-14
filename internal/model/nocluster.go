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

package model

// The reason words of what a project worked without a cluster loses, each a
// row of docs/plans/no-cluster.md's loss table. theos status prints the ones
// that apply.
const (
	LossNoFence                   = "NoFence"
	LossOwnModelCredential        = "OwnModelCredential" //nolint:gosec // a reason word
	LossNoCheckpoint              = "NoCheckpoint"
	LossCostUnreported            = "CostUnreported"
	LossPersonMerges              = "PersonMerges"
	LossAgentHoldsOwnerCredential = "AgentHoldsOwnerCredential"
	LossReviewNotEnforced         = "ReviewNotEnforced"
	LossChecksByMerge             = "ChecksByMerge"
	LossProtectedPathsByMerge     = "ProtectedPathsByMerge"
	LossRulesNotValidated         = "RulesNotValidated"
)

// NoClusterLoss is one thing a cluster gives that a project without one
// does not have: its reason word and what is in its place.
type NoClusterLoss struct {
	Reason string
	Says   string
}

// NoClusterLosses are the losses that apply to a project on host (a
// GitProvider's provider) whose agents hold the credential that protected
// main when ownerCredential is true, in the loss table's order.
func NoClusterLosses(host string, ownerCredential bool) []NoClusterLoss {
	losses := []NoClusterLoss{
		{LossNoFence, "no NetworkPolicy, ServiceAccount or deadline holds a session: only its runtime's own sandbox"},
		{LossOwnModelCredential, "each runtime holds its own model subscription or key"},
		{LossNoCheckpoint, "a session that dies leaves its commits on its branch; the next claim continues there"},
		{LossCostUnreported, "no cost or tokens are recorded; the host's issues, pull requests and reviews are the record"},
		{LossPersonMerges, "a person merges every pull request, in the host's pages or from a terminal"},
	}
	if ownerCredential {
		losses = append(losses, NoClusterLoss{LossAgentHoldsOwnerCredential, "the sessions hold the credential " +
			"that protected main: it merges past the rule, can change or remove it, and reaches every repository " +
			"the token does; a second account's token narrows it"})
	}
	losses = append(losses, NoClusterLoss{LossReviewNotEnforced,
		"no host is asked for a review, and a session's name is its own word"})
	if host == "github" {
		losses = append(losses,
			NoClusterLoss{LossChecksByMerge, "the host does not hold the required checks at a merge in its pages"},
			NoClusterLoss{LossProtectedPathsByMerge, "the host does not hold the protected paths at a merge in its pages"})
	}
	return append(losses, NoClusterLoss{LossRulesNotValidated,
		"the CRDs' create-time rules are checked here; the admission policy, the rules that compare an update with the object before it and the GitProvider provider enum (the host is checked by theos itself) run only when the model reaches a cluster"})
}
