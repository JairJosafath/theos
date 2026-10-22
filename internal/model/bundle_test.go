package model

import (
	"errors"
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

func TestInBundle(t *testing.T) {
	typeErr := CheckChoices("web-app", []CatalogEntry{{Side: SideFramework, Name: "go-cli", SupportedTypes: []string{"cli"}}})
	fitErr := CheckStandard("go-cli", &CatalogEntry{Side: SideStandard, Name: "python-standard",
		SupportedFrameworks: []string{"python-fastapi"}})
	selfErr := CheckDeploysItself(CatalogEntry{Side: SideFramework, Name: "aws-cdk", DeploysItself: true},
		"web-app", "small", "")
	for _, err := range []error{typeErr, fitErr, selfErr} {
		if err == nil {
			t.Fatal("a check passed that should fail")
		}
	}
	tests := []struct {
		name   string
		bundle string
		err    error
		want   string
	}{
		{"unsupported type", "go-tool", typeErr, "bundle go-tool: " + typeErr.Error()},
		{"standard doesn't fit", "mixed", fitErr, "bundle mixed: " + fitErr.Error()},
		{"deploys itself", "cdk-app", selfErr, "bundle cdk-app: " + selfErr.Error()},
		{"no bundle", "", fitErr, fitErr.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InBundle(tt.bundle, tt.err)
			if got.Error() != tt.want {
				t.Errorf("InBundle() = %q, want %q", got, tt.want)
			}
			if !errors.Is(got, tt.err) {
				t.Errorf("InBundle() lost the error underneath")
			}
		})
	}
	if _, ok := errors.AsType[*FitError](InBundle("mixed", fitErr)); !ok {
		t.Error("InBundle() hides the FitError the reason is read from")
	}
	if _, ok := errors.AsType[*SelfDeployError](InBundle("cdk-app", selfErr)); !ok {
		t.Error("InBundle() hides the SelfDeployError the reason is read from")
	}
	if InBundle("go-tool", nil) != nil {
		t.Error("InBundle(nil) is not nil")
	}
}
