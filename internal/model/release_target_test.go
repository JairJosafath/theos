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
	"strings"
	"testing"
)

func TestReleaseTarget(t *testing.T) {
	const a, b = `{"github":"a"}`, `{"github":"b"}`
	tests := []struct {
		name        string
		entry, mark string
		marked      bool
		crew        string
		want        TargetAction
	}{
		{name: "in sync", entry: a, mark: a, marked: true, crew: a, want: TargetInSync},
		{name: "entry changed", entry: b, mark: a, marked: true, crew: a, want: TargetWrite},
		{name: "entry changed while the Crew was changed: the newest act wins", entry: b, mark: a, marked: true,
			crew: b, want: TargetWrite},
		{name: "entry changed while held at another", entry: b, mark: a, marked: true, crew: `{"github":"c"}`,
			want: TargetWrite},
		{name: "Crew changed under the Brief", entry: a, mark: a, marked: true, crew: b, want: TargetHold},
		{name: "Crew's target removed under the Brief", entry: a, mark: a, marked: true, crew: "", want: TargetHold},
		{name: "unmarked Crew with no target is adopted", entry: a, crew: "", want: TargetWrite},
		{name: "unmarked Crew with the entry's target is adopted", entry: a, crew: a, want: TargetWrite},
		{name: "unmarked Crew with another target is held", entry: a, crew: b, want: TargetHold},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReleaseTarget(tt.entry, tt.mark, tt.marked, tt.crew); got != tt.want {
				t.Errorf("ReleaseTarget = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReleaseTargetMessages(t *testing.T) {
	d := TargetDiffers{Brief: "cel-lens", Crew: "linter", BriefRepo: "acme/cel-lens", CrewRepo: "acme/private"}
	if got, want := ReleaseTargetDiffersMessage(d), "The Crew's spec.publishReleases points to acme/private, "+
		"but Brief cel-lens's spec.releases entry for linter says acme/cel-lens; nothing is published until they "+
		"agree. Change the Brief's entry to the target wanted, or put the Crew's spec.publishReleases back"; got != want {
		t.Errorf("ReleaseTargetDiffersMessage =\n%s\nwant\n%s", got, want)
	}
	n := ReleaseTargetNeedsYou(d)
	if n.Kind != AttentionCrew || n.Name != "linter" ||
		n.Recommend != "Change the Brief's spec.releases entry for linter to the Crew's target." ||
		n.Why != "The Crew was changed to acme/private but the Brief still says acme/cel-lens; nothing is "+
			"published until they agree." {
		t.Errorf("ReleaseTargetNeedsYou = %+v", n)
	}
	same := d
	same.CrewRepo, same.SameRepository = d.BriefRepo, true
	if got := ReleaseTargetPublishMessage(same); !strings.Contains(got, "acme/cel-lens with other settings") {
		t.Errorf("ReleaseTargetPublishMessage = %s, want the same repository said to have other settings", got)
	}
	nowhere := d
	nowhere.CrewRepo, nowhere.PublishesNowhere = "", true
	if n := ReleaseTargetNeedsYou(nowhere); !strings.Contains(n.Question, "to nowhere") ||
		!strings.HasPrefix(n.Recommend, "Remove the Brief's spec.releases entry") {
		t.Errorf("ReleaseTargetNeedsYou of a Crew publishing nowhere = %+v", n)
	}
	if got := ReleaseTargetSetByBrief("cel-lens"); got != "Brief cel-lens sets this in spec.releases; change it there" {
		t.Errorf("ReleaseTargetSetByBrief = %s", got)
	}
}

func TestInsteadOf(t *testing.T) {
	stuck := NeedsYou{Kind: AttentionCrew, Name: "linter", Question: "not ready"}
	issue := NeedsYou{Kind: AttentionIssue, Name: "linter#1"}
	held := NeedsYou{Kind: AttentionCrew, Name: "linter", Question: "held"}
	got := InsteadOf(Attention{NeedsYou: []NeedsYou{stuck, issue}, Waiting: 2}, []NeedsYou{held})
	if got.Waiting != 2 || len(got.NeedsYou) != 2 || got.NeedsYou[0] != issue || got.NeedsYou[1] != held {
		t.Errorf("InsteadOf = %+v, want the issue and the held Crew, 2 waiting", got)
	}
}
