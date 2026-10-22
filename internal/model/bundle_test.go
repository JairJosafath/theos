package model

import (
	"slices"
	"testing"
)

func TestBundleChoices(t *testing.T) {
	tests := []struct {
		name   string
		bundle Bundle
		want   []BundleChoice
	}{
		{"all four sides", Bundle{Name: "python-service", Framework: "python-fastapi", Standard: "python-standard",
			Compute: "small", Deployment: "k8s-manifests"}, []BundleChoice{
			{SideFramework, "python-fastapi"}, {SideStandard, "python-standard"},
			{SideCompute, "small"}, {SideDeployment, "k8s-manifests"},
		}},
		{"no compute", Bundle{Name: "go-cli", Framework: "go-cli", Standard: "go-standard", Deployment: "gitea-release"},
			[]BundleChoice{{SideFramework, "go-cli"}, {SideStandard, "go-standard"}, {SideDeployment, "gitea-release"}}},
		{"framework only", Bundle{Name: "bare", Framework: "react-vite"},
			[]BundleChoice{{SideFramework, "react-vite"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BundleChoices(tt.bundle); !slices.Equal(got, tt.want) {
				t.Errorf("BundleChoices() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBundleSupportedTypes(t *testing.T) {
	fastapi := entry(SideFramework, "python-fastapi", TypeService, TypeWorker)
	standard := entry(SideStandard, "python-standard", TypeService, TypeWorker, TypeJob, TypeCLI)
	small := entry(SideCompute, "small", TypeService, TypeWorker, TypeJob)
	manifests := entry(SideDeployment, "k8s-manifests", TypeWorker, TypeService)
	vite := entry(SideFramework, "react-vite", TypeStaticSite)

	tests := []struct {
		name    string
		entries []CatalogEntry
		want    []string
	}{
		{"the intersection, in the first entry's order", []CatalogEntry{fastapi, standard, small, manifests},
			[]string{TypeService, TypeWorker}},
		{"one entry supports what it lists", []CatalogEntry{standard},
			[]string{TypeService, TypeWorker, TypeJob, TypeCLI}},
		{"narrowed by a later entry", []CatalogEntry{standard, entry(SideCompute, "tiny", TypeJob)},
			[]string{TypeJob}},
		{"entries that share no type support none", []CatalogEntry{vite, small, manifests}, nil},
		{"a type listed twice counts once", []CatalogEntry{entry(SideFramework, "dup", TypeService, TypeService), small},
			[]string{TypeService}},
		{"no entries support none", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BundleSupportedTypes(tt.entries); !slices.Equal(got, tt.want) {
				t.Errorf("BundleSupportedTypes() = %v, want %v", got, tt.want)
			}
		})
	}
}
