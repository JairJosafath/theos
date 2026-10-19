package model

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestDeployApproval(t *testing.T) {
	tests := []struct {
		tier, approval, want string
	}{
		{tier: DeployTierProduction, want: DeployApprovalRequired},
		{tier: DeployTierDevelopment, want: DeployApprovalNone},
		// A target stored before the CRD defaulted its tier is development.
		{tier: "", want: DeployApprovalNone},
		{tier: DeployTierProduction, approval: DeployApprovalNone, want: DeployApprovalNone},
		{tier: DeployTierDevelopment, approval: DeployApprovalRequired, want: DeployApprovalRequired},
		{tier: "", approval: DeployApprovalRequired, want: DeployApprovalRequired},
	}
	for _, tt := range tests {
		t.Run(tt.tier+"/"+tt.approval, func(t *testing.T) {
			if got := DeployApproval(tt.tier, tt.approval); got != tt.want {
				t.Errorf("DeployApproval(%q, %q) = %q, want %q", tt.tier, tt.approval, got, tt.want)
			}
		})
	}
}

func TestDeployMembers(t *testing.T) {
	tests := []struct {
		name     string
		member   string
		c        DeployContainer
		wantKeys []string
		wantErr  bool
	}{
		// A GitHub release needs nothing of a Container but its releases.
		{name: "github, a bare Container", member: DeployMemberGitHub, wantKeys: []string{"token"}},
		{name: "github, with a Deployment", member: DeployMemberGitHub, c: DeployContainer{Deployment: "k8s-manifests"},
			wantKeys: []string{"token"}},
		{name: "github, deploying itself", member: DeployMemberGitHub, c: DeployContainer{DeploysItself: true},
			wantKeys: []string{"token"}},
		// A member is added with its adapter, never before.
		{name: "cloudFormation not yet", member: "cloudFormation", wantErr: true},
		{name: "no member", member: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := DeployMemberByName(tt.member)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DeployMemberByName(%q) error = %v, want error %v", tt.member, err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if m.Name != tt.member || !slices.Equal(m.SecretKeys, tt.wantKeys) {
				t.Errorf("member = %+v, want name %s and keys %v", m, tt.member, tt.wantKeys)
			}
			if lacks := m.Lacks(tt.c); len(lacks) != 0 {
				t.Errorf("Lacks(%+v) = %v, want nothing", tt.c, lacks)
			}
		})
	}
	if got := DeployMemberNames(); !slices.Equal(got, []string{DeployMemberGitHub}) {
		t.Errorf("DeployMemberNames() = %v", got)
	}
}

func TestDeployMemberLacks(t *testing.T) {
	m := DeployMember{Name: "test", Needs: []DeployNeed{
		{What: "a Deployment that writes deploy/k8s/", Met: func(c DeployContainer) bool { return c.Deployment != "" }},
		{What: "a Framework that deploys itself", Met: func(c DeployContainer) bool { return c.DeploysItself }},
	}}
	tests := []struct {
		name string
		c    DeployContainer
		want []string
	}{
		{name: "has both", c: DeployContainer{Deployment: "k8s-manifests", DeploysItself: true}},
		{name: "lacks one", c: DeployContainer{DeploysItself: true}, want: []string{"a Deployment that writes deploy/k8s/"}},
		{name: "lacks both, in the table's order", want: []string{"a Deployment that writes deploy/k8s/",
			"a Framework that deploys itself"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.Lacks(tt.c); !slices.Equal(got, tt.want) {
				t.Errorf("Lacks(%+v) = %v, want %v", tt.c, got, tt.want)
			}
		})
	}
}

func TestDeployMemberMissingKeys(t *testing.T) {
	m := DeployMember{SecretKeys: []string{"token", "role"}}
	tests := []struct {
		name string
		data map[string][]byte
		want []string
	}{
		{name: "both", data: map[string][]byte{"token": []byte("t"), "role": []byte("r")}},
		{name: "none", want: []string{"token", "role"}},
		{name: "blank counts as missing", data: map[string][]byte{"token": []byte(" \n"), "role": []byte("r")},
			want: []string{"token"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := m.MissingKeys(tt.data); !slices.Equal(got, tt.want) {
				t.Errorf("MissingKeys = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeployCheckMessage(t *testing.T) {
	github, _ := DeployMemberByName(DeployMemberGitHub)
	c := DeployCheck{Target: "prod", Secret: "prod-github", Where: "acme/shop", Member: github}
	tests := []struct {
		reason string
		c      DeployCheck
		want   []string
	}{
		{reason: DeployTargetReady, c: c, want: []string{"GitHub accepts", "prod-github", "acme/shop"}},
		{reason: DeployTargetSecretMissing, c: c,
			want: []string{"Secret prod-github, with the key token, is missing", "DeployTarget prod", "acme/shop"}},
		{reason: DeployTargetSecretIncomplete, c: DeployCheck{Target: "prod", Secret: "prod-github", Where: "acme/shop",
			Member: github, Missing: []string{"token"}}, want: []string{"has no key token", "DeployTarget prod"}},
		// Names the target and who sets the Secret, as the release copy does.
		{reason: DeployTargetCredentialRefused, c: DeployCheck{Target: "prod", Secret: "prod-github",
			Where: "acme/shop", Member: github, Detail: "Bad credentials (HTTP 401)"},
			want: []string{"GitHub refused the credential in Secret prod-github of DeployTarget prod for acme/shop: " +
				"Bad credentials (HTTP 401)", "Whoever owns DeployTarget prod sets Secret prod-github",
				"Contents read and write"}},
		{reason: DeployTargetUnreachable, c: DeployCheck{Target: "prod", Secret: "prod-github", Where: "acme/shop",
			Member: github, Detail: "HTTP 502"}, want: []string{"GitHub could not be reached", "DeployTarget prod",
			"HTTP 502"}},
	}
	for _, tt := range tests {
		t.Run(tt.reason, func(t *testing.T) {
			got := DeployCheckMessage(tt.reason, tt.c)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("DeployCheckMessage(%q) = %q, want it to hold %q", tt.reason, got, w)
				}
			}
		})
	}
}

// The draft, upload and publish sequence the github member's adapter and the
// Crew's release copy share.
func TestNextReleaseStep(t *testing.T) {
	files := []ReleaseAsset{{ID: 0, Name: "app.tar.gz", Size: 10}, {ID: 1, Name: "SHA256SUMS", Size: 2}}
	ship := ReleaseShip{Tag: "v1.0.0", Name: "Orders v1.0.0", Body: "notes\n\n<!-- m -->\n", Files: files,
		Latest: true}
	draft := func(target string, assets ...ReleaseAsset) *TargetRelease {
		return &TargetRelease{ID: 7, Draft: true, Target: target, Assets: assets}
	}
	tests := []struct {
		name string
		r    ReleaseShip
		ours *TargetRelease
		want PublishStep
	}{
		{name: "no draft: create it at the default branch", r: ship, want: PublishStep{Action: PublishCreateDraft,
			Tag: "v1.0.0", Name: "Orders v1.0.0", Body: ship.Body}},
		{name: "no draft: create it at the commit", r: ReleaseShip{Tag: "v1.0.0", Commit: "abc"},
			want: PublishStep{Action: PublishCreateDraft, Tag: "v1.0.0", Target: "abc"}},
		{name: "a draft at another commit is retargeted", r: ReleaseShip{Commit: "abc", Files: files},
			ours: draft("main"), want: PublishStep{Action: PublishRetargetDraft, Release: 7, Target: "abc"}},
		{name: "a draft is left where it is when no commit is wanted", r: ship, ours: draft("main"),
			want: PublishStep{Action: PublishUploadAsset, Release: 7, Asset: files[0]}},
		{name: "the next missing file", r: ship, ours: draft("", ReleaseAsset{ID: 9, Name: "app.tar.gz", Size: 10}),
			want: PublishStep{Action: PublishUploadAsset, Release: 7, Asset: files[1]}},
		{name: "a file of another size is replaced", r: ship,
			ours: draft("", ReleaseAsset{ID: 9, Name: "app.tar.gz", Size: -1}),
			want: PublishStep{Action: PublishUploadAsset, Release: 7, Asset: files[0],
				Replace: &ReleaseAsset{ID: 9, Name: "app.tar.gz", Size: -1}}},
		{name: "every file there: publish", r: ship, ours: draft("", files...),
			want: PublishStep{Action: PublishDraft, Release: 7, Latest: true}},
		{name: "published: done", r: ship, ours: &TargetRelease{ID: 7, URL: "https://github.com/o/r/releases/tag/v1.0.0"},
			want: PublishStep{Action: PublishDone, URL: "https://github.com/o/r/releases/tag/v1.0.0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NextReleaseStep(tt.r, tt.ours); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NextReleaseStep() = %+v, want %+v", got, tt.want)
			}
		})
	}
	if got := ReleaseBody("", "<!-- m -->"); got != "<!-- m -->\n" {
		t.Errorf("ReleaseBody without notes = %q", got)
	}
	if got := ReleaseBody("notes\n\n", "<!-- m -->"); got != "notes\n\n<!-- m -->\n" {
		t.Errorf("ReleaseBody = %q", got)
	}
	if got := PublishMarker("ns", "app", "v1"); got != ShipMarker("ns/app", "v1") ||
		!strings.HasPrefix(got, ShipMarkerPrefix("ns/app")) {
		t.Errorf("PublishMarker = %q, ShipMarker = %q", got, ShipMarker("ns/app", "v1"))
	}
}
