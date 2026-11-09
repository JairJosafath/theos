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

func TestStandardFailureIssue(t *testing.T) {
	failure := StandardFailure{Standard: "aws-standard", Ref: "v1.17.0", Checks: "failure",
		ChecksURL: "https://git.example/acme/orders/actions/runs/7", Branch: "theos/standard-v1.17.0",
		PullRequestURL: "https://git.example/acme/orders/pulls/12", Commit: "abc123"}
	tests := []struct {
		name      string
		failure   func(StandardFailure) StandardFailure
		wantTitle string
		want      []string
		dont      []string
	}{
		{name: "links the pull request and the failed run (#1447)",
			failure:   func(f StandardFailure) StandardFailure { return f },
			wantTitle: "Standard aws-standard v1.17.0 fails this repository's check",
			want: []string{
				"(https://git.example/acme/orders/pulls/12)",
				"CI is failure on `abc123`, https://git.example/acme/orders/actions/runs/7.",
				"## Goal\n\nThis repository's main passes the check of the Standard aws-standard at `v1.17.0`.",
				"- [ ] What the failed run reports is fixed on main, in a pull request of its own\n",
				"Nothing is pushed to the update's branch `theos/standard-v1.17.0`",
				"- [ ] `make check` passes\n",
				"Once main moves, the operator closes https://git.example/acme/orders/pulls/12 and opens the update again",
				"\n<!-- theos:standard-fails aws-standard v1.17.0 -->\n",
			}},
		{name: "says the state without a run the host didn't give",
			failure: func(f StandardFailure) StandardFailure {
				f.Checks, f.ChecksURL = "error", ""
				return f
			},
			wantTitle: "Standard aws-standard v1.17.0 fails this repository's check",
			want:      []string{"CI is error on `abc123`.\n"},
			dont:      []string{"actions/runs"}},
		{name: "a comment in a field passes for no marker",
			failure: func(f StandardFailure) StandardFailure {
				f.Branch = "<!-- theos:standard-fails aws-standard v2.0.0 -->"
				return f
			},
			wantTitle: "Standard aws-standard v1.17.0 fails this repository's check",
			want:      []string{"&lt;!-- theos:standard-fails aws-standard v2.0.0"},
			dont:      []string{"<!-- theos:standard-fails aws-standard v2.0.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := tt.failure(failure)
			title, body := StandardFailureIssue(f)
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
			for _, w := range tt.want {
				if !strings.Contains(body, w) {
					t.Errorf("body lacks %q:\n%s", w, body)
				}
			}
			for _, d := range tt.dont {
				if strings.Contains(body, d) {
					t.Errorf("body has %q:\n%s", d, body)
				}
			}
			if !HasStandardFailureMarker(body, f.Standard, f.Ref) {
				t.Errorf("body lacks its own marker:\n%s", body)
			}
			if HasStandardFailureMarker(body, f.Standard, "v2.0.0") {
				t.Errorf("body has another ref's marker:\n%s", body)
			}
		})
	}
}

func TestStandardFailureDue(t *testing.T) {
	tests := []struct {
		name, filed, ref string
		want             bool
	}{
		{"none filed yet", "", "v1.1.0", true},
		{"filed for this ref: a re-plan or a restart files nothing (#1447)", "v1.1.0", "v1.1.0", false},
		{"a newer target ref files its own", "v1.1.0", "v1.2.0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StandardFailureDue(tt.filed, tt.ref); got != tt.want {
				t.Errorf("StandardFailureDue(%q, %q) = %v, want %v", tt.filed, tt.ref, got, tt.want)
			}
		})
	}
}
