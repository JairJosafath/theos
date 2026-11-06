package model

import (
	"reflect"
	"slices"
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
		{"#2025: an empty release alone is no release yet, so nothing is done or held",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, nil, nil)}},
			ReleaseStep{}},
		{"#2025: an empty release before one with merged work closes untagged as the later one ships",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(22, "v0.22.0", 12, nil, nil),
				milestone(23, "v0.23.0", 19, nil, []int{120})},
				Merged: []MergedTask{merged("t-120", 120, "c120", 8)}},
			ReleaseStep{Tag: "v0.23.0", Commit: "c120", Milestone: 23, CreateTag: true,
				Skipped: []EmptiedRelease{{ID: 22, Title: "v0.22.0"}}}},
		{"#2025: an empty release doesn't hold a later one in work, nor say a done one waits for it",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(22, "v0.22.0", 12, nil, nil),
				milestone(23, "v0.23.0", 19, []int{121}, []int{120}), milestone(24, "v0.24.0", 26, nil, nil)},
				Merged: []MergedTask{merged("t-120", 120, "c120", 8)}},
			ReleaseStep{}},
		{"#2025: an empty release after the one tagged stays open, for the work still to come",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(23, "v0.23.0", 19, nil, []int{120}),
				milestone(24, "v0.24.0", 26, nil, nil)},
				Merged: []MergedTask{merged("t-120", 120, "c120", 8)}},
			ReleaseStep{Tag: "v0.23.0", Commit: "c120", Milestone: 23, CreateTag: true}},
		{"#2025: an empty release before one held keeps open, and the hold names the later one",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(22, "v0.22.0", 12, nil, nil),
				milestone(23, "v0.23.0", 19, nil, []int{46})}},
			ReleaseStep{Tag: "v0.23.0", Reason: ReleaseNoMergedIssue, Held: "v0.23.0 is done but none of its issues was merged by a Task; a person tags it"}},
		{"a merge without its commit holds the release",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020},
				Merged: append(serverMerges[1:], merged("server-44", 44, "", 3))},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseNoMergeCommit, Held: "v0.2.0 waits for the merge commit of Task server-44 (#44); " +
				"a person moves #44 out of v0.2.0 to release it without"}},
		{"ties on the merge time break on the Task's name",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, nil, []int{1, 2})},
				Merged: []MergedTask{merged("t-2", 2, "c2", 3), merged("t-1", 1, "c1", 3)}},
			ReleaseStep{Tag: "v0.2.0", Commit: "c2", Milestone: 2, CreateTag: true}},
		{"a later release's merge before the newest one moves into the release it tags",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, []int{121}, []int{120})},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 5))},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true,
				Moved: []ReleaseMove{{Issue: 120, From: "v0.3.0"}}}},
		{"a later release's merges before the newest one move, from each later release, and one left empty closes",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, nil, []int{120, 122}),
				milestone(4, "v0.4.0", 26, nil, []int{130, 131})},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 5), merged("server-122", 122, "ccc1222", 4),
					merged("server-130", 130, "ccc1300", 2), merged("server-131", 131, "ccc1310", 7))},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true,
				Moved:   []ReleaseMove{{Issue: 120, From: "v0.3.0"}, {Issue: 122, From: "v0.3.0"}, {Issue: 130, From: "v0.4.0"}},
				Emptied: []EmptiedRelease{{ID: 3, Title: "v0.3.0"}}}},
		{"a later issue closed by hand or merged again after the newest one stays",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, nil, []int{120, 123})},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 5),
					merged("server-120-again", 120, "ccc1201", 7))},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
		{"a later release's prepare issue stays",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020,
				{ID: 3, Release: Release{Title: "v0.3.0", Due: day(19)}, Closed: []int{120, 124}, Prepared: []int{124}}},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 5), merged("server-124", 124, "ccc1240", 5))},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true,
				Moved: []ReleaseMove{{Issue: 120, From: "v0.3.0"}}}},
		{"an empty later release is left alone",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, nil, nil)}, Merged: serverMerges},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
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
		{"a tag already at the commit still moves the later work it carries",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, []int{121}, []int{120})},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 5)),
				Tags:   map[string]string{"v0.2.0": "694db57"}},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, Moved: []ReleaseMove{{Issue: 120, From: "v0.3.0"}}}},
		{"a later release's reopened issue that merged early stays in its release",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, []int{120}, nil)},
				Merged: append(serverMerges, merged("server-120", 120, "ccc1200", 5))},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
		{"cel-lens v0.1.3: its prepare issue merged after v0.2.0's fixes, which move into it",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{
				{ID: 1, Release: Release{Title: "v0.1.3", Due: day(5)}, Closed: []int{30, 31, 135}, Prepared: []int{135}},
				milestone(2, "v0.2.0", 12, nil, []int{40, 41, 42, 43}),
				milestone(3, "v0.3.0", 19, []int{137}, []int{136})},
				Active: []int{137},
				Merged: []MergedTask{merged("core-30", 30, "c30", 1), merged("core-40", 40, "c40", 2),
					merged("core-41", 41, "c41", 2), merged("core-42", 42, "c42", 3), merged("core-43", 43, "c43", 3),
					merged("core-31", 31, "c31", 4), merged("core-135", 135, "c135", 5), merged("core-136", 136, "c136", 6)}},
			ReleaseStep{Tag: "v0.1.3", Commit: "c135", Milestone: 1, CreateTag: true,
				Moved: []ReleaseMove{{Issue: 40, From: "v0.2.0"}, {Issue: 41, From: "v0.2.0"}, {Issue: 42, From: "v0.2.0"},
					{Issue: 43, From: "v0.2.0"}},
				Emptied: []EmptiedRelease{{ID: 2, Title: "v0.2.0"}}}},
		{"a merge without its commit doesn't hold when a later try of the issue has one",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020},
				Merged: append(serverMerges, merged("server-44-old", 44, "", 2))},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
		{"an issue its Standard change did counts as merged at the change's merge",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, nil, []int{44, 46, 205})},
				Merged: []MergedTask{merged("server-44", 44, "aaa4444", 3), merged("server-205", 205, "116291b", 4),
					{Name: "server-46", Issue: 46, At: day(5), DoneBy: 205}}},
			ReleaseStep{Tag: "v0.2.0", Commit: "116291b", Milestone: 2, CreateTag: true}},
		{"an issue done by a Standard change of a later release carries the change",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, nil, []int{44, 46}),
				milestone(3, "v0.3.0", 19, nil, []int{205})},
				Merged: []MergedTask{merged("server-44", 44, "aaa4444", 3), merged("t-205", 205, "116291b", 4),
					{Name: "a-46", Issue: 46, At: day(5), DoneBy: 205}}},
			ReleaseStep{Tag: "v0.2.0", Commit: "116291b", Milestone: 2, CreateTag: true,
				Moved: []ReleaseMove{{Issue: 205, From: "v0.3.0"}}, Emptied: []EmptiedRelease{{ID: 3, Title: "v0.3.0"}}}},
		{"an issue done by a Standard change whose merge is lost doesn't hold the release",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, nil, []int{44, 46})},
				Merged: []MergedTask{merged("server-44", 44, "aaa4444", 3),
					{Name: "server-46", Issue: 46, At: day(5), DoneBy: 205}}},
			ReleaseStep{Tag: "v0.2.0", Commit: "aaa4444", Milestone: 2, CreateTag: true}},
		{"an issue done by a Standard change whose merge waits for its commit holds the release",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, nil, []int{44, 46})},
				Merged: []MergedTask{merged("server-44", 44, "aaa4444", 3), merged("t-205", 205, "", 4),
					{Name: "server-46", Issue: 46, At: day(5), DoneBy: 205}}},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseNoMergeCommit, Held: "v0.2.0 waits for the merge commit of " +
				"Task t-205 (#205), the Standard change that did #46; a person moves #46 out of v0.2.0 to release it " +
				"without"}},
		{"a Standard change's later try with its commit releases the issue it did",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, nil, []int{44, 46})},
				Merged: []MergedTask{merged("server-44", 44, "aaa4444", 3), merged("t-205-old", 205, "", 2),
					merged("t-205", 205, "116291b", 4), {Name: "server-46", Issue: 46, At: day(5), DoneBy: 205}}},
			ReleaseStep{Tag: "v0.2.0", Commit: "116291b", Milestone: 2, CreateTag: true}},
		{"a merge without its time holds the release",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020},
				Merged: append(serverMerges[1:], MergedTask{Name: "server-44", Issue: 44, Commit: "aaa4444"})},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleaseNoMergeCommit, Held: "v0.2.0 waits for the merge commit of Task " +
				"server-44 (#44); a person moves #44 out of v0.2.0 to release it without"}},
		{"the waiting message names the first release",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(2, "v0.2.0", 12, []int{99}, nil),
				milestone(3, "v0.3.0", 19, []int{120}, nil), milestone(4, "v0.4.0", 26, nil, []int{130})},
				Merged: []MergedTask{merged("t-130", 130, "c130", 3)}},
			ReleaseStep{Tag: "v0.4.0", Reason: ReleaseWaiting, Held: "v0.4.0 is done and waits for v0.2.0 to be released"}},
		{"a Task at work on a later release doesn't hold a done one",
			ReleaseFacts{Milestones: []ReleaseMilestone{v020, milestone(3, "v0.3.0", 19, []int{120}, nil)},
				Merged: serverMerges, Active: []int{120}},
			ReleaseStep{Tag: "v0.2.0", Commit: "694db57", Milestone: 2, CreateTag: true}},
		// The engine's v0.3.0: its newest merge, #136, came before v0.2.0's
		// prepare issue #140 merged and was tagged.
		{"a release whose newest merge came before the previous tag is held",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(3, "v0.3.0", 19, nil, []int{136})},
				Merged:   []MergedTask{merged("engine-136", 136, "c136", 3), merged("engine-140", 140, "c140", 5)},
				Previous: &ReleasedTag{Release: Release{Title: "v0.2.0", Due: day(12)}, Commit: "c140"}},
			ReleaseStep{Tag: "v0.3.0", Reason: ReleaseBehindPrevious, Held: "v0.3.0 would be tagged at c136, the " +
				"merge of #136, but v0.2.0's tag names c140, the merge of #140, which came after it; a release's " +
				"tag must come after the one before it, so a person tags it"}},
		{"a release at the previous tag's commit is held",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(3, "v0.3.0", 19, nil, []int{136})},
				Merged:   []MergedTask{merged("engine-136", 136, "c136", 3)},
				Previous: &ReleasedTag{Release: Release{Title: "v0.2.0", Due: day(12)}, Commit: "c136"}},
			ReleaseStep{Tag: "v0.3.0", Reason: ReleaseBehindPrevious, Held: "v0.3.0 would be tagged at c136, the " +
				"merge of #136, but v0.2.0's tag names that commit already; a release's tag must come after the " +
				"one before it, so a person tags it"}},
		{"a release whose newest merge came after the previous tag is tagged",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(3, "v0.3.0", 19, nil, []int{136, 153})},
				Merged: []MergedTask{merged("engine-136", 136, "c136", 3), merged("engine-140", 140, "c140", 5),
					merged("engine-153", 153, "c153", 8)},
				Previous: &ReleasedTag{Release: Release{Title: "v0.2.0", Due: day(12)}, Commit: "c140"}},
			ReleaseStep{Tag: "v0.3.0", Commit: "c153", Milestone: 3, CreateTag: true}},
		{"a previous tag at a commit no Task merged doesn't hold it",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(3, "v0.3.0", 19, nil, []int{136})},
				Merged:   []MergedTask{merged("engine-136", 136, "c136", 3)},
				Previous: &ReleasedTag{Release: Release{Title: "v0.2.0", Due: day(12)}, Commit: "byhand"}},
			ReleaseStep{Tag: "v0.3.0", Commit: "c136", Milestone: 3, CreateTag: true}},
		{"a closed release after this one isn't the previous",
			ReleaseFacts{Milestones: []ReleaseMilestone{milestone(3, "v0.3.0", 19, nil, []int{136})},
				Merged:   []MergedTask{merged("engine-136", 136, "c136", 3), merged("engine-140", 140, "c140", 5)},
				Previous: &ReleasedTag{Release: Release{Title: "v0.4.0", Due: day(26)}, Commit: "c140"}},
			ReleaseStep{Tag: "v0.3.0", Commit: "c136", Milestone: 3, CreateTag: true}},
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

func TestPreviousReleases(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	v010, v020, v040 := Release{"v0.1.0", day(5)}, Release{"v0.2.0", day(12)}, Release{"v0.4.0", day(26)}
	open := []ReleaseMilestone{{Release: Release{"v0.3.0", day(19)}}}
	// v0.4.0 was closed ahead of v0.3.0, by hand: it isn't before v0.3.0.
	if got := PreviousReleases([]Release{v040, v010, v020}, open, 5); !reflect.DeepEqual(got, []Release{v020, v010}) {
		t.Errorf("PreviousReleases() = %v, want v0.2.0 then v0.1.0", got)
	}
	if got := PreviousReleases([]Release{v010, v020}, open, 1); !reflect.DeepEqual(got, []Release{v020}) {
		t.Errorf("PreviousReleases() with limit 1 = %v, want v0.2.0", got)
	}
	if got := PreviousReleases([]Release{v010}, nil, 5); !reflect.DeepEqual(got, []Release{v010}) {
		t.Errorf("PreviousReleases() with no open release = %v, want v0.1.0", got)
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
		{"the prepare issue lists the later releases' merged work its tag will carry",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1, 2, 3}, nil),
				{ID: 3, Release: Release{Title: "v0.3.0", Due: day(19)}, Open: []int{9}, Closed: []int{5, 6, 8}}},
				Merged: append(work, merged(6, "c6", 2), merged(5, "c5", 4))},
			ReleaseStep{Tag: "v0.2.0", Milestone: 2, File: true, Merged: []int{1, 2, 3, 6, 5}}},
		{"a later release's Task in review holds the prepare issue, so its work merges first (#956)",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1, 2, 3}, nil),
				{ID: 3, Release: Release{Title: "v0.3.0", Due: day(19)}, Open: []int{332}}},
				Merged: work, Active: []int{332}, Working: []string{"cel-332-2", "by-hand"}},
			ReleaseStep{Tag: "v0.2.0", Reason: ReleasePrepareWaits, Held: "v0.2.0 is done; its prepare issue waits " +
				"for Tasks cel-332-2 and by-hand to end, so it is built on the main their work merges into"}},
		{"once that Task merged, the prepare issue lists its work",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1, 2, 3}, nil),
				{ID: 3, Release: Release{Title: "v0.3.0", Due: day(19)}, Closed: []int{332}}},
				Merged: append(slices.Clone(work), merged(332, "c332", 6))},
			ReleaseStep{Tag: "v0.2.0", Milestone: 2, File: true, Merged: []int{1, 2, 3, 332}}},
		{"a Task at work doesn't hold a release that already has its prepare issue",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1, 2, 3, 4}, []int{4})},
				Merged: append(slices.Clone(work), merged(4, "c4", 6)), Working: []string{"cel-9"}},
			ReleaseStep{Tag: "v0.2.0", Commit: "c4", Milestone: 2, CreateTag: true}},
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
		{"cel-lens v0.6.0: an issue its Standard change did before the prepare issue doesn't hold it",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1, 46, 205, 215}, []int{215})},
				Merged: []MergedTask{merged(1, "c1", 3), merged(205, "116291b", 4),
					{Name: "t-46", Issue: 46, At: day(5), DoneBy: 205}, merged(215, "89d96e4", 6)}},
			ReleaseStep{Tag: "v0.2.0", Commit: "89d96e4", Milestone: 2, CreateTag: true}},
		{"its prepare issue lists it in the order its change merged",
			ReleaseFacts{Prepare: true, Milestones: []ReleaseMilestone{release(nil, []int{1, 46, 205}, nil)},
				Merged: []MergedTask{{Name: "t-46", Issue: 46, At: day(5), DoneBy: 205}, merged(205, "116291b", 2),
					merged(1, "c1", 3)}},
			ReleaseStep{Tag: "v0.2.0", Milestone: 2, File: true, Merged: []int{205, 46, 1}}},
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

func TestReleaseRunResult(t *testing.T) {
	queued := ReleaseRun{Workflow: "release.yaml", Status: "queued", URL: "/runs/5"}
	running := ReleaseRun{Workflow: "release.yaml", Status: "in_progress", URL: "/runs/5"}
	passed := ReleaseRun{Workflow: "release.yaml", Status: "completed", Conclusion: "success", URL: "/runs/5"}
	skipped := ReleaseRun{Workflow: "notes.yaml", Status: "completed", Conclusion: "skipped", URL: "/runs/6"}
	failed := ReleaseRun{Workflow: "release.yaml", Status: "completed", Conclusion: "failure", URL: "/runs/7"}
	notes := ReleaseRun{Workflow: "notes.yaml", Status: "in_progress", URL: "/runs/9"}
	cancelled := ReleaseRun{Workflow: "release.yaml", Status: "completed", Conclusion: "cancelled", URL: "/runs/8"}
	tests := []struct {
		name       string
		runs       []ReleaseRun
		wantResult string
		wantRun    ReleaseRun
	}{
		{"no run started yet", nil, RunWaiting, ReleaseRun{}},
		{"a queued run", []ReleaseRun{queued}, RunRunning, queued},
		{"a running run", []ReleaseRun{running}, RunRunning, running},
		{"a run that passed", []ReleaseRun{passed}, RunSucceeded, passed},
		{"a skipped run counts as ended well", []ReleaseRun{skipped, passed}, RunSucceeded, skipped},
		{"one still running among passed", []ReleaseRun{passed, notes}, RunRunning, passed},
		{"a failed run, even with another running", []ReleaseRun{notes, failed}, RunFailed, failed},
		{"a cancelled run is a failure", []ReleaseRun{cancelled, passed}, RunFailed, cancelled},
		{"a workflow's newer run passed after an older one failed", []ReleaseRun{passed, failed}, RunSucceeded, passed},
		{"a workflow's newer run failed after an older one passed", []ReleaseRun{failed, passed}, RunFailed, failed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, run := ReleaseRunResult(tt.runs)
			if result != tt.wantResult || run != tt.wantRun {
				t.Errorf("ReleaseRunResult() = %s, %+v; want %s, %+v", result, run, tt.wantResult, tt.wantRun)
			}
		})
	}
}

func TestFailedReleaseRuns(t *testing.T) {
	passed := ReleaseRun{ID: 5, Workflow: "release.yaml", Status: "completed", Conclusion: "success"}
	failed := ReleaseRun{ID: 7, Workflow: "release.yaml", Status: "completed", Conclusion: "failure"}
	cancelled := ReleaseRun{ID: 8, Workflow: "notes.yaml", Status: "completed", Conclusion: "cancelled"}
	skipped := ReleaseRun{ID: 6, Workflow: "notes.yaml", Status: "completed", Conclusion: "skipped"}
	running := ReleaseRun{ID: 9, Workflow: "notes.yaml", Status: "in_progress"}
	tests := []struct {
		name string
		runs []ReleaseRun
		want []ReleaseRun
	}{
		{"no runs", nil, nil},
		{"all ended well", []ReleaseRun{passed, skipped}, nil},
		{"a failed and a cancelled run", []ReleaseRun{cancelled, failed}, []ReleaseRun{cancelled, failed}},
		{"one failed, one still running", []ReleaseRun{running, failed}, []ReleaseRun{failed}},
		{"an older failure a newer run passed", []ReleaseRun{passed, failed}, nil},
		{"a newer failure after a pass", []ReleaseRun{failed, passed}, []ReleaseRun{failed}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FailedReleaseRuns(tt.runs); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FailedReleaseRuns() = %+v; want %+v", got, tt.want)
			}
		})
	}
}

func TestReleaseRerun(t *testing.T) {
	tests := []struct {
		name, value, answered, run, fullCheck string
		want                                  string
	}{
		{"no annotation", "", "", RunFailed, "", RerunNone},
		{"a new value on a failed run", "1", "", RunFailed, "", RerunRuns},
		{"a changed value on a failed run", "2", "1", RunFailed, "", RerunRuns},
		{"the answered value again", "1", "1", RunFailed, "", RerunNone},
		{"a new value on a run that succeeded", "1", "", RunSucceeded, "", RerunNothingFailed},
		{"a new value on a running run", "1", "", RunRunning, "", RerunNothingFailed},
		{"a new value with no release", "1", "", "", "", RerunNothingFailed},
		{"#2028: a new value while a failed full check holds the next release", "1", "", RunSucceeded, RunFailed,
			RerunFullCheck},
		{"#2028: the failed full check goes before a failed release run", "1", "", RunFailed, RunFailed,
			RerunFullCheck},
		{"#2028: the answered value again leaves the full check", "1", "1", "", RunFailed, RerunNone},
		{"#2028: a full check that runs is nothing to re-run", "1", "", "", RunRunning, RerunNothingFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReleaseRerun(tt.value, tt.answered, tt.run, tt.fullCheck); got != tt.want {
				t.Errorf("ReleaseRerun(%q, %q, %q, %q) = %q; want %q", tt.value, tt.answered, tt.run,
					tt.fullCheck, got, tt.want)
			}
		})
	}
}

func TestReleaseRunLate(t *testing.T) {
	tagged := time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		result string
		after  time.Duration
		want   bool
	}{
		{"waiting within the bound", RunWaiting, 59 * time.Minute, false},
		{"waiting at the bound", RunWaiting, time.Hour, true},
		{"running past the bound", RunRunning, 90 * time.Minute, true},
		{"succeeded past the bound", RunSucceeded, 2 * time.Hour, false},
		{"failed past the bound", RunFailed, 2 * time.Hour, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReleaseRunLate(tt.result, tagged, tagged.Add(tt.after)); got != tt.want {
				t.Errorf("ReleaseRunLate(%s, +%s) = %v; want %v", tt.result, tt.after, got, tt.want)
			}
		})
	}
}

func TestReleaseBumpable(t *testing.T) {
	tagged := time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		result string
		after  time.Duration
		want   bool
	}{
		{"succeeded at once", RunSucceeded, time.Minute, true},
		{"waiting within the bound", RunWaiting, 59 * time.Minute, false},
		{"waiting at the bound", RunWaiting, time.Hour, true},
		{"never read within the bound", "", 59 * time.Minute, false},
		{"never read past the bound", "", 2 * time.Hour, true},
		{"running within the bound", RunRunning, time.Minute, false},
		{"running past the bound", RunRunning, 2 * time.Hour, false},
		{"failed", RunFailed, time.Minute, false},
		{"failed past the bound", RunFailed, 2 * time.Hour, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReleaseBumpable(tt.result, false, false, tagged, tagged.Add(tt.after)); got != tt.want {
				t.Errorf("ReleaseBumpable(%q, +%s) = %v; want %v", tt.result, tt.after, got, tt.want)
			}
		})
	}
}

func TestReleaseBumpablePublished(t *testing.T) {
	tagged := time.Date(2026, 10, 6, 1, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		result    string
		published bool
		after     time.Duration
		want      bool
	}{
		{"succeeded, not yet published", RunSucceeded, false, time.Minute, false},
		{"succeeded and published", RunSucceeded, true, time.Minute, true},
		{"no run past the bound, not published", RunWaiting, false, 2 * time.Hour, false},
		{"never read past the bound, not published", "", false, 2 * time.Hour, false},
		{"failed, not published", RunFailed, false, time.Minute, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ReleaseBumpable(tt.result, true, tt.published, tagged, tagged.Add(tt.after)); got != tt.want {
				t.Errorf("ReleaseBumpable(%q, published %v, +%s) = %v; want %v",
					tt.result, tt.published, tt.after, got, tt.want)
			}
		})
	}
}

func TestReleasedMessage(t *testing.T) {
	step := ReleaseStep{Tag: "v0.1.3", Commit: "c135"}
	if got, want := ReleasedMessage(step), "Released v0.1.3 at c135"; got != want {
		t.Errorf("ReleasedMessage() = %q, want %q", got, want)
	}
	step.Moved = []ReleaseMove{{Issue: 40, From: "v0.2.0"}, {Issue: 41, From: "v0.2.0"}}
	step.Emptied = []EmptiedRelease{{ID: 2, Title: "v0.2.0"}}
	want := "Released v0.1.3 at c135, carrying #40 of v0.2.0, #41 of v0.2.0; v0.2.0 had nothing left and closed untagged"
	if got := ReleasedMessage(step); got != want {
		t.Errorf("ReleasedMessage() = %q, want %q", got, want)
	}
	step.Skipped = []EmptiedRelease{{ID: 1, Title: "v0.1.2"}}
	if got, want := ReleasedMessage(step), want+"; v0.1.2 had no issues and closed untagged"; got != want {
		t.Errorf("ReleasedMessage() with a skipped release (#2025) = %q, want %q", got, want)
	}
	if got := MovedComment(step, step.Moved[0]); !strings.Contains(got, "Moved from v0.2.0 to v0.1.3") ||
		!strings.Contains(got, "c135") {
		t.Errorf("MovedComment() = %q", got)
	}
}
