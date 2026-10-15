package model

import (
	"slices"
	"testing"
)

// giteaActions is the Gitea Actions row, the CI system the rules' tests use.
var giteaActions = mustCI(CIGiteaActions)

func mustCI(name string) CI {
	ci, err := CIByName(name)
	if err != nil {
		panic(err)
	}
	return ci
}

func TestHostCI(t *testing.T) {
	tests := []struct {
		host                string
		wantName            string
		wantContexts, paths []string
		wantErr             bool
	}{
		{host: "gitea", wantName: CIGiteaActions, wantContexts: []string{"check*"},
			paths: []string{".c4factory.yaml", ".devcontainer/", ".gitea/"}},
		{host: "github", wantErr: true},
		{host: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			ci, err := HostCI(tt.host)
			if (err != nil) != tt.wantErr {
				t.Fatalf("HostCI(%q) error = %v, want error %v", tt.host, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if ci.Name != tt.wantName || !slices.Equal(ci.CheckContexts, tt.wantContexts) {
				t.Errorf("HostCI(%q) = %+v, want %s with contexts %v", tt.host, ci, tt.wantName, tt.wantContexts)
			}
			if got := PlatformProtectedPaths(ci); !slices.Equal(got, tt.paths) {
				t.Errorf("PlatformProtectedPaths(%s) = %v, want %v", ci.Name, got, tt.paths)
			}
		})
	}
}

func TestCIByNameUnknown(t *testing.T) {
	if _, err := CIByName("jenkins"); err == nil {
		t.Error("CIByName(jenkins) = nil error, want unknown")
	}
}

// TestCIRulesTakeTheCI shows the merge rules read the check contexts and the
// protected paths from the CI system they are given, not from Gitea Actions.
func TestCIRulesTakeTheCI(t *testing.T) {
	other := CI{Name: "other", CheckContexts: []string{"build"}, ProtectedPaths: []string{".other-ci.yml"}}
	passing := map[string]string{"build": "success"}
	giteaPassing := map[string]string{"check / check (pull_request)": "success"}
	tests := []struct {
		name     string
		ci       CI
		contexts map[string]string
		want     string
	}{
		{"gitea actions passes its own context", giteaActions, giteaPassing, ""},
		{"gitea actions refuses another's", giteaActions, passing, `no passing check matches "check*", which main requires`},
		{"another CI passes its own context", other, passing, ""},
		{"another CI refuses gitea actions'", other, giteaPassing, `no passing check matches "build", which main requires`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StandardUpdateRefusal(tt.ci, checkSuccess, tt.contexts, []string{"Makefile"},
				[]string{"Makefile"}); got != tt.want {
				t.Errorf("StandardUpdateRefusal() = %q, want %q", got, tt.want)
			}
			if got := GatedMergeRefusal(tt.ci, checkSuccess, tt.contexts, VerdictApprove, []string{"Makefile"},
				[]string{"Makefile"}, AgentProtectedPaths(tt.ci, []string{"Makefile"})); got != tt.want {
				t.Errorf("GatedMergeRefusal() = %q, want %q", got, tt.want)
			}
		})
	}
	if got := GatedMergeRefusal(CI{}, checkSuccess, giteaPassing, VerdictApprove, []string{"Makefile"},
		[]string{"Makefile"}, nil); got != "no CI system names the checks main requires" {
		t.Errorf("GatedMergeRefusal(no CI) = %q, want a refusal", got)
	}
	if got := FactoryOwnPaths(other); slices.Contains(got, ".gitea/") || !slices.Contains(got, ".other-ci.yml") {
		t.Errorf("FactoryOwnPaths(other) = %v, want .other-ci.yml and no .gitea/", got)
	}
}
