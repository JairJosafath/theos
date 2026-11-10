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

// #98: a Crew files one issue, once, on a repository it adopted with no
// Standard, and only where agents may work.
func TestFilesOnboarding(t *testing.T) {
	tests := []struct {
		name  string
		facts OnboardingFacts
		want  bool
	}{
		{"an adopted, protected repository", OnboardingFacts{Adopted: true}, true},
		{"a scaffolded one has its Standard's guides", OnboardingFacts{}, false},
		{"not while main isn't protected", OnboardingFacts{Adopted: true, Unprotected: "no check matching check*"}, false},
		{"not while the Crew is paused", OnboardingFacts{Adopted: true, Paused: true}, false},
		{"never a second", OnboardingFacts{Adopted: true, Filed: true}, false},
	}
	for _, tt := range tests {
		if got := FilesOnboarding(tt.facts); got != tt.want {
			t.Errorf("%s: FilesOnboarding() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestOnboardingIssue(t *testing.T) {
	title, body := OnboardingIssue(false)
	if title != "Write AGENTS.md for this repository, and say what you found" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{"This repository has no `AGENTS.md`. Write it.", "## Goal", "Change no other file.",
		"## Acceptance criteria\n\n- [ ] ", "- [ ] The pull request changes no file but `AGENTS.md`\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("the issue lacks %q:\n%s", want, body)
		}
	}
	if !HasOnboardingMarker(body) || !strings.HasSuffix(body, "\n") {
		t.Errorf("the issue doesn't end in its marker:\n%s", body)
	}
	// The Crew builds only an issue with an Acceptance criteria checklist.
	if missing := MissingAcceptanceCriteria(body); missing != "" {
		t.Errorf("MissingAcceptanceCriteria() = %q, want none: the Crew would hand the issue back", missing)
	}

	title, body = OnboardingIssue(true)
	if title != "Check and complete AGENTS.md, and say what you found" ||
		!strings.Contains(body, "This repository has an `AGENTS.md`. Check each thing it says") {
		t.Errorf("for a repository with a guide: %q\n%s", title, body)
	}

	for body, want := range map[string]bool{
		"Text.\n<!-- theos-onboarding -->\n":     true,
		"Text.\n  <!-- theos-onboarding -->  \n": true,
		"Text about <!-- theos-onboarding -->.":  false,
		"<!-- theos-schedule: dependencies -->":  false,
		"":                                       false,
	} {
		if got := HasOnboardingMarker(body); got != want {
			t.Errorf("HasOnboardingMarker(%q) = %v, want %v", body, got, want)
		}
	}
	if label := OnboardingLabel(); label.Name != LabelOnboarding || label.Exclusive {
		t.Errorf("OnboardingLabel() = %+v", label)
	}
}
