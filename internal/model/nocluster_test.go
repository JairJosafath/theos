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

import (
	"slices"
	"strings"
	"testing"
)

// #268: the losses theos status prints, by host and by whose credential the
// sessions hold (docs/plans/no-cluster.md, the loss table).
func TestNoClusterLosses(t *testing.T) {
	always := []string{LossNoFence, LossOwnModelCredential, LossNoCheckpoint, LossCostUnreported, LossPersonMerges}
	cases := []struct {
		name  string
		host  string
		owner bool
		want  []string
	}{
		{"#268 GitHub, the owner's credential", "github", true, append(slices.Clone(always),
			LossAgentHoldsOwnerCredential, LossReviewNotEnforced, LossChecksByMerge, LossProtectedPathsByMerge,
			LossRulesNotValidated)},
		{"#268 GitHub, a second account's", "github", false, append(slices.Clone(always), LossReviewNotEnforced,
			LossChecksByMerge, LossProtectedPathsByMerge, LossRulesNotValidated)},
		{"#268 a host that holds checks and paths at the merge", Hosts()[0], true, append(slices.Clone(always),
			LossAgentHoldsOwnerCredential, LossReviewNotEnforced, LossRulesNotValidated)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, l := range NoClusterLosses(c.host, c.owner) {
				if l.Says == "" {
					t.Errorf("%s says nothing", l.Reason)
				}
				got = append(got, l.Reason)
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("losses %v, want %v", got, c.want)
			}
		})
	}
	// #268: the rules loss says what is not checked without a cluster, and
	// not that the CRDs' enums or CEL rules are all unchecked.
	for _, l := range NoClusterLosses("github", true) {
		if l.Reason != LossRulesNotValidated {
			continue
		}
		for _, part := range []string{"create-time rules are checked here", "admission policy",
			"compare an update with the object before it", "GitProvider provider enum"} {
			if !strings.Contains(l.Says, part) {
				t.Errorf("%s says %q, want it to contain %q", l.Reason, l.Says, part)
			}
		}
	}
	if hosts := Hosts(); len(hosts) != 2 || hosts[1] != "github" {
		t.Errorf("Hosts() = %v", hosts)
	}
}
