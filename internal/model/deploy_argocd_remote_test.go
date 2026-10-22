package model

import (
	"strings"
	"testing"
)

// TestArgoCDRepoURL covers docs/plans/k8s-deploy.md's decision 9 (#1590): the
// Git host's address as the other cluster sees it replaces the clone URL's.
func TestArgoCDRepoURL(t *testing.T) {
	clone := "http://gitea-http.gitea.svc.cluster.local:3000/acme/orders-api.git"
	tests := []struct {
		name, gitURL, want, wantErr string
	}{
		{"no gitURL keeps the clone URL", "", clone, ""},
		{"gitURL replaces the scheme and host", "https://git.example.com",
			"https://git.example.com/acme/orders-api.git", ""},
		{"a port stays", "https://git.example.com:8443/", "https://git.example.com:8443/acme/orders-api.git", ""},
		{"a path is refused", "https://git.example.com/gitea", "", "is not scheme://host[:port]"},
		{"no scheme is refused", "git.example.com", "", "is not scheme://host[:port]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ArgoCDRepoURL(clone, tt.gitURL)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ArgoCDRepoURL(%q) error = %v, want %q", tt.gitURL, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ArgoCDRepoURL(%q) = %q, %v; want %q", tt.gitURL, got, err, tt.want)
			}
		})
	}
}

// TestArgoCDImageCopyOf covers decision 9's copy (#1590): the image keeps its
// repository and digest below the imageRegistry, path included, under the
// nested layout, and only its name under the flat one (#1770).
func TestArgoCDImageCopyOf(t *testing.T) {
	const image = "gitea.gitea.svc.cluster.local:3000/acme/orders-api@sha256:abc"
	tests := []struct {
		name, image, registry, layout string
		want                          ArgoCDImageCopy
		wantErr                       string
	}{
		{"a host alone", image, "registry.example.com", "", ArgoCDImageCopy{
			FromHost: "gitea.gitea.svc.cluster.local:3000", FromRepository: "acme/orders-api", Digest: "sha256:abc",
			ToHost: "registry.example.com", ToRepository: "acme/orders-api",
			Image: "registry.example.com/acme/orders-api@sha256:abc"}, ""},
		{"a host, port and path", image, "10.0.0.5:5000/team/apps/", ArgoCDLayoutNested, ArgoCDImageCopy{
			FromHost: "gitea.gitea.svc.cluster.local:3000", FromRepository: "acme/orders-api", Digest: "sha256:abc",
			ToHost: "10.0.0.5:5000", ToRepository: "team/apps/acme/orders-api",
			Image: "10.0.0.5:5000/team/apps/acme/orders-api@sha256:abc"}, ""},
		{"flat under a Docker Hub user (#1770)", image, "docker.io/myuser", ArgoCDLayoutFlat, ArgoCDImageCopy{
			FromHost: "gitea.gitea.svc.cluster.local:3000", FromRepository: "acme/orders-api", Digest: "sha256:abc",
			ToHost: "docker.io", ToRepository: "myuser/orders-api",
			Image: "docker.io/myuser/orders-api@sha256:abc"}, ""},
		{"flat at a host alone (#1770)", image, "registry.lab:5000", ArgoCDLayoutFlat, ArgoCDImageCopy{
			FromHost: "gitea.gitea.svc.cluster.local:3000", FromRepository: "acme/orders-api", Digest: "sha256:abc",
			ToHost: "registry.lab:5000", ToRepository: "orders-api",
			Image: "registry.lab:5000/orders-api@sha256:abc"}, ""},
		{"an unknown layout (#1770)", image, "registry.example.com", "deep", ArgoCDImageCopy{},
			`imageRepositoryLayout "deep" is not nested or flat`},
		{"an image without a digest", "registry.example.com/acme/orders-api:v1", "r.example.com", "",
			ArgoCDImageCopy{}, "is not host/repository@digest"},
		{"a registry with a scheme", image, "https://registry.example.com", "", ArgoCDImageCopy{},
			"is not host[:port][/path]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ArgoCDImageCopyOf(tt.image, tt.registry, tt.layout)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ArgoCDImageCopyOf = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}
