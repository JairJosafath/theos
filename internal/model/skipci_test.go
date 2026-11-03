package model

import "testing"

// #2060: a squash merge of a green head that is up to date skips main's push run.
func TestSkipsPushRun(t *testing.T) {
	required := []string{"check / check (pull_request)"}
	green := map[string]string{"check / check (pull_request)": "success"}
	tests := []struct {
		name     string
		required []string
		state    string
		contexts map[string]string
		behind   bool
		want     bool
	}{
		{"green and up to date", required, "success", green, false, true},
		{"green, no required patterns", nil, "success", green, false, true},
		{"green but behind main", required, "success", green, true, false},
		{"failed", required, "failure", map[string]string{"check / check (pull_request)": "failure"}, false, false},
		{"pending", required, "pending", map[string]string{"check / check (pull_request)": "pending"}, false, false},
		{"no required check ran", required, "success", map[string]string{"other": "success"}, false, false},
		{"no checks at all", required, "", nil, false, false},
		{"required green, another failed", required, "failure",
			map[string]string{"check / check (pull_request)": "success", "lint": "failure"}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SkipsPushRun(tt.required, tt.state, tt.contexts, tt.behind); got != tt.want {
				t.Errorf("SkipsPushRun = %v, want %v", got, tt.want)
			}
		})
	}
}

// #2060: the marker is in the title, which a host reads first.
func TestSquashTitle(t *testing.T) {
	if got := SquashTitle("Add x", true); got != "Add x [skip ci]" {
		t.Errorf("skipping title = %q", got)
	}
	if got := SquashTitle("Add x", false); got != "Add x" {
		t.Errorf("title = %q", got)
	}
}

func TestRequiredChecks(t *testing.T) {
	contexts := map[string]string{"check / b (pull_request)": "success", "check / a (pull_request)": "success",
		"check / c (pull_request)": "skipped", "lint": "success"}
	got := RequiredChecks([]string{"check / *"}, contexts)
	if len(got) != 2 || got[0] != "check / a (pull_request)" || got[1] != "check / b (pull_request)" {
		t.Errorf("RequiredChecks = %v", got)
	}
	if got := RequiredChecks(nil, contexts); len(got) != 3 {
		t.Errorf("without patterns = %v", got)
	}
	if got := SameTreeDescription(7, "abc"); got != "same tree as #7 head abc" {
		t.Errorf("description = %q", got)
	}
}
