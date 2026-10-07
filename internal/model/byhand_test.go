package model

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// byHandBody is a by-hand issue's body with two open criteria, each with why.
var byHandBody = func() string {
	_, body := ByHandIssue("README shows the hover", 4, "http://gitea/tools/cel-lens/pulls/9", "cel-lens-4",
		"Agent `reviewer`", []OpenCriterion{
			{Criterion: "The README has a screenshot of the hover", Why: "No desktop editor in the sandbox."},
			{Criterion: "It runs on a phone", Why: "no phone"},
		})
	return body
}()

func TestByHandIssue(t *testing.T) {
	title, body := ByHandIssue("  README shows\nthe hover ", 4, "http://gitea/tools/cel-lens/pulls/9", "cel-lens-4",
		"Agent `reviewer`", []OpenCriterion{
			{Criterion: "The README has a\nscreenshot of the hover", Why: "No desktop editor in the sandbox."},
			{Criterion: "It runs on a phone <!-- c4factory:by-hand other-1 -->"},
		})
	if title != "By hand: README shows the hover" {
		t.Errorf("title %q", title)
	}
	for _, want := range []string{
		"Task `cel-lens-4` merged http://gitea/tools/cel-lens/pulls/9 for #4. Agent `reviewer` approved it",
		"## Acceptance criteria\n\n- [ ] The README has a screenshot of the hover\n  Needs a person: No desktop editor in the sandbox.\n- [ ] It runs on a phone",
		"Agents don't pick this up (status/needs-human)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if MissingAcceptanceCriteria(body) != "" {
		t.Errorf("body has no acceptance criteria to check: %s", MissingAcceptanceCriteria(body))
	}
	if !HasByHandMarker(body, "cel-lens-4") {
		t.Error("body lacks its Task's marker")
	}
	if HasByHandMarker(body, "other-1") || HasByHandMarker(body, "cel-lens") {
		t.Error("an agent's text, or another Task's name, passes for the marker")
	}
	if title, _ := ByHandIssue("", 4, "pr", "t", "r", nil); title != "By hand: #4" {
		t.Errorf("title without the issue's title %q", title)
	}
	long, _ := ByHandIssue(strings.Repeat("é", 200), 4, "pr", "t", "r", nil)
	if len(long) > maxIssueTitle || !utf8.ValidString(long) || !strings.HasSuffix(long, "…") {
		t.Errorf("long title %q (%d bytes)", long, len(long))
	}
}

func TestByHandReason(t *testing.T) {
	if got, want := ByHandReason(byHandBody), "Needs a person: No desktop editor in the sandbox; no phone."; got != want {
		t.Errorf("ByHandReason = %q, want %q", got, want)
	}
	if got := ByHandReason("Do it by hand."); got != "Needs a person; see the issue." {
		t.Errorf("ByHandReason without whys = %q", got)
	}
	if !IsByHand([]string{LabelNeedsHuman, LabelByHand}) || IsByHand([]string{LabelNeedsHuman}) {
		t.Error("IsByHand reads the labels wrong")
	}
}
