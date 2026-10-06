package model

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIsReleaseTitle(t *testing.T) {
	for title, want := range map[string]bool{
		"v0.2.0": true, "v10.0.12": true,
		"0.2.0": false, "v0.2": false, "v0.2.0-rc1": false, "Later": false, "v0.2.0 ": false, "v01.2.3": false,
	} {
		if got := IsReleaseTitle(title); got != want {
			t.Errorf("IsReleaseTitle(%q) = %v, want %v", title, got, want)
		}
	}
}

func TestNextRelease(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	milestone := func(id int64, title string, due int, open, closed []int) ReleaseMilestone {
		return ReleaseMilestone{ID: id, Release: Release{Title: title, Due: day(due)}, Open: open, Closed: closed}
	}
	merged := func(name string, issue int, commit string, at int) MergedTask {
		return MergedTask{Name: name, Issue: issue, Commit: commit, At: day(at)}
	}
	// The server's history: v0.2.0 (#44 #45 #49 #50) was held by #99, handed back
	// to a person; a person then moved #99 and #120 to v0.3.0.
	serverMerges := []MergedTask{
		merged("server-44", 44, "aaa4444", 3), merged("server-45", 45, "694db57", 6),
		merged("server-49", 49, "aaa4949", 4), merged("server-50", 50, "aaa5050", 5),
	}
	v020 := milestone(2, "v0.2.0", 12, nil, []int{44, 45, 49, 50})

	tests := []struct {
		name  string
		facts ReleaseFacts
		want  ReleaseStep
	}{
		{"no releases, nothing to do", ReleaseFacts{}, ReleaseStep{}},
		{"server v0.2.0 is held while #99 is with a person",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, []int{99}, v020.Closed)},
				Merged: serverMerges},
			ReleaseStep{}},
		{"server v0.2.0 is tagged at its newest merge once #99 moved on",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, []int{99, 120}, nil)},
				Merged: serverMerges},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
		{"server v0.3.0 is tagged at #99's merge",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(3, "v0.3.0", 19, nil, []int{99, 120})},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 8), merged("server-99", 99, "1cedb81", 9))},
			ReleaseStep{Tag: "v0.3.0", Commit: "1cedb81", Milestone: 3, CreateTag: true}},
		{"a Task at work holds the release",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020}, Merged: serverMerges, Active: []int{50}},
			ReleaseStep{}},
		{"a Task at work on an issue of another release doesn't",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020}, Merged: serverMerges, Active: []int{7}},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
		{"a done later release waits for the earlier one",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(3, "v0.3.0", 19, nil, []int{120}),
				milestone(2, "v0.2.0", 12, []int{99}, v020.Closed)},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 8))},
			ReleaseStep{Tag: "v0.3.0", Reason: ReleaseWaiting, Held: "v0.3.0 is done and waits for v0.2.0 to be released"}},
		{"releases go in due-date order, title breaking a tie",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(4, "v0.2.1", 12, nil, []int{1}),
				milestone(5, "v0.2.0", 12, nil, []int{2})},
				Merged: []MergedTask{merged("t-1", 1, "c1", 5), merged("t-2", 2, "c2", 4)}},
			ReleaseStep{Tag: "v0.2.0", Commit: "c2", Milestone: 5, CreateTag: true}},
		{"milestones without a version title are not releases",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(1, "Sprint 1", 1, []int{3}, nil), v020},
				Merged: serverMerges},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
		{"issues closed by hand don't count",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, nil, []int{44, 46})},
				Merged: []MergedTask{merged("server-44", 44, "aaa4444", 3)}},
			ReleaseStep{Tag: "v0.2.0", Commit: "aaa4444", Milestone: 2, CreateTag: true}},
		{"a done release with no merged issue is left to a person",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, nil, []int{46})}},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseNoMergedIssue, Held: "v0.2.0 is done but none of its issues was merged by a Task; a person tags it"}},
		{"an empty release is left to a person",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, nil, nil)}},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseNoMergedIssue, Held: "v0.2.0 is done but none of its issues was merged by a Task; a person tags it"}},
		{"a merge without its commit holds the release",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020},
				Merged: append(serverMerges[1:], merged("server-44", 44, "", 3))},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseNoMergeCommit, Held: "v0.2.0 waits for the merge commit of Task server-44 (#44)"}},
		{"ties on the merge time break on the Task's name",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, nil, []int{1, 2})},
				Merged: []MergedTask{merged("t-2", 2, "c2", 3), merged("t-1", 1, "c1", 3)}},
			ReleaseStep{Tag: "v0.2.0", Commit: "c2", Milestone: 2, CreateTag: true}},
		{"a later release's merge before the newest one is left to a person",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, []int{121}, []int{120})},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 5))},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseLaterWork, Held: "v0.2.0's newest merge (#45) came after #120 of v0.3.0 merged, so its tag would " +
				"hold later work; a person tags it"}},
		{"a later release's merge after the newest one is fine",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, nil, []int{120})},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 7))},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
		{"a tag at the same commit only closes the milestone",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020}, Merged: serverMerges,
				Tags: map[string]string{"v0.2.0": "694db57"}},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2}},
		{"a tag at another commit is a clash",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020}, Merged: serverMerges,
				Tags: map[string]string{"v0.2.0": "1234567"}},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseTagClash, Held: "v0.2.0 is done at 694db57 but the tag v0.2.0 names 1234567; a person resolves it"}},
		{"a tag a person made at the same commit closes the milestone despite later work",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, []int{121}, []int{120})},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 5)),
				Tags:   map[string]string{"v0.2.0": "694db57"}},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2}},
		{"a later release's reopened issue that merged early still counts",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, []int{120}, nil)},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 5))},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseLaterWork, Held: "v0.2.0's newest merge (#45) came after #120 of " +
				"v0.3.0 merged, so its tag would hold later work; a person tags it"}},
		{"a merge without its commit doesn't hold when a later try of the issue has one",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020},
				Merged: append(serverMerges, merged("server-44-old", 44, "", 2))},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
		{"a merge without its time holds the release",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020},
				Merged: append(serverMerges[1:], MergedTask{Name: "server-44", Issue: 44, Commit: "aaa4444"})},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseNoMergeCommit, Held: "v0.2.0 waits for the merge commit of Task " +
				"server-44 (#44)"}},
		{"the waiting message names the first release",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, []int{99}, nil),
				milestone(3, "v0.3.0", 19, []int{120}, nil), milestone(4, "v0.4.0", 26, nil, []int{130})},
				Merged: []MergedTask{merged("t-130", 130, "c130", 3)}},
			ReleaseStep{Tag: "v0.4.0", Reason: ReleaseWaiting, Held: "v0.4.0 is done and waits for v0.2.0 to be released"}},
		{"a Task at work on a later release doesn't hold a done one",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, []int{120}, nil)},
				Merged: serverMerges, Active: []int{120}},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
		{"a milestone without a due date is no release",
			ReleaseFacts{Milestones: []ReleaseMilestone{{ID: 9, Release: Release{Title: "v0.1.0"}, Open: []int{5}}, v020},
				Merged: serverMerges},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextRelease(tt.facts); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NextRelease() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNextReleasePrepares(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	release := func(open, closed, prepared []int) ReleaseMilestone {
		return ReleaseMilestone{ID: 2, Release: Release{Title: "v0.2.0", Due: day(12)}, Open: open, Closed: closed,
			Prepared: prepared}
	}
	merged := func(issue int, commit string, at int) MergedTask {
		return MergedTask{Name: "t-" + commit, Issue: issue, Commit: commit, At: day(at)}
	}
	work := make([]MergedTask, 0, 4)
	work = append(work, merged(3, "c3", 5), merged(1, "c1", 3), merged(2, "c2", 4))

	tests := []struct {
		name  string
		facts ReleaseFacts
		want  ReleaseStep
	}{
		{"a done release with merged work gets its prepare issue, merges oldest first",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1, 2, 3, 4}, nil)}, Merged: work},
			ReleaseStep{Tag: "v0.2.0", Milestone: 2, File: true, Merged: []int{1, 2, 3}}},
		{"without the switch it is tagged as before",
			ReleaseFacts{Milestones: []ReleaseMilestone{release(nil, []int{1, 2, 3}, nil)}, Merged: work},
			ReleaseStep{Tag: "v0.2.0", Commit: "c3", Milestone: 2, CreateTag: true}},
		{"a merge waiting for its commit still counts as work",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1}, nil)},
				Merged: []MergedTask{{Name: "t-1", Issue: 1}}},
			ReleaseStep{Tag: "v0.2.0", Milestone: 2, File: true, Merged: []int{1}}},
		{"a later try of an issue is listed once",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1}, nil)},
				Merged: []MergedTask{merged(1, "old", 2), merged(1, "new", 3)}},
			ReleaseStep{Tag: "v0.2.0", Milestone: 2, File: true, Merged: []int{1}}},
		{"an open release gets none",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release([]int{4}, []int{1, 2, 3}, nil)},
				Merged: work},
			ReleaseStep{}},
		{"an open prepare issue holds the release like any issue",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release([]int{7}, []int{1, 2, 3}, []int{7})},
				Merged: work},
			ReleaseStep{}},
		{"a release with no merged work gets none",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{4}, nil)}},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseNoMergedIssue, Held: "v0.2.0 is done but none of its issues was " +
				"merged by a Task; a person tags it"}},
		{"only the first release gets one",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release([]int{9}, nil, nil),
				{ID: 3, Release: Release{Title: "v0.3.0", Due: day(19)}, Closed: []int{1}}},
				Merged: []MergedTask{merged(1, "c1", 3)}},
			ReleaseStep{Tag: "v0.3.0", Reason: ReleaseWaiting, Held: "v0.3.0 is done and waits for v0.2.0 to be released"}},
		{"a merged prepare issue is the tag",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1, 2, 3, 7}, []int{7})},
				Merged: append(work, merged(7, "c7", 6))},
			ReleaseStep{Tag: "v0.2.0", Commit: "c7", Milestone: 2, CreateTag: true}},
		{"a prepare issue closed by hand is the person's call",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1, 2, 3, 7}, []int{7})},
				Merged: work},
			ReleaseStep{Tag: "v0.2.0", Commit: "c3", Milestone: 2, CreateTag: true}},
		{"work merged after the prepare issue is left to a person",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1, 2, 3, 7}, []int{7})},
				Merged: []MergedTask{merged(1, "c1", 3), merged(7, "c7", 4), merged(3, "c3", 5)}},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseMergedAfterPrepare, Held: "v0.2.0's #3 merged after its prepare " +
				"issue #7, so its changelog misses it; a person files another kind/release issue in v0.2.0 or tags it"}},
		{"another prepare issue after the late work clears it",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1, 3, 7, 8}, []int{7, 8})},
				Merged: []MergedTask{merged(1, "c1", 3), merged(7, "c7", 4), merged(3, "c3", 5), merged(8, "c8", 6)}},
			ReleaseStep{Tag: "v0.2.0", Commit: "c8", Milestone: 2, CreateTag: true}},
		{"without the switch late work is tagged as before",
			ReleaseFacts{Milestones: []ReleaseMilestone{release(nil, []int{1, 3, 7}, []int{7})},
				Merged: []MergedTask{merged(1, "c1", 3), merged(7, "c7", 4), merged(3, "c3", 5)}},
			ReleaseStep{Tag: "v0.2.0", Commit: "c3", Milestone: 2, CreateTag: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextRelease(tt.facts); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NextRelease() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestPrepareIssue(t *testing.T) {
	title, body := PrepareIssue("v0.2.0", []int{3, 1}, map[int]string{1: "Add a greeting", 3: "Say it in Dutch"})
	if title != "Prepare v0.2.0" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{
		"- #3 Say it in Dutch\n- #1 Add a greeting\n",
		"`make release-version VERSION=v0.2.0`",
		"## Acceptance criteria\n\n- [ ] ",
		"0.2.0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body misses %q:\n%s", want, body)
		}
	}
	if msg := MissingAcceptanceCriteria(body); msg != "" {
		t.Errorf("the prepare issue is not ready as filed: %s", msg)
	}
}
