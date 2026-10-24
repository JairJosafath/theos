package model

import (
	"slices"
	"testing"
)

func TestScaffoldedDomains(t *testing.T) {
	others := []string{"python-fastapi", "python-standard", "small", "k8s-manifests", "postgres"}
	tests := []struct {
		name           string
		scaffoldedFrom []string
		want           []string
	}{
		{"none", []string{"python-fastapi@v1", "small@v1", "k8s-manifests@v1", "postgres@v1"}, nil},
		{"two, sorted", []string{"python-fastapi@v1", "postgres@v1", "webshop@v0.1.0", "billing@v0.2.0"},
			[]string{"billing", "webshop"}},
		{"only the trailing run counts", []string{"renamed-framework@v1", "small@v1", "postgres@v1", "webshop@v0.1.0"},
			[]string{"webshop"}},
		{"adopted Container has none", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ScaffoldedDomains(tt.scaffoldedFrom, others); !slices.Equal(got, tt.want) {
				t.Errorf("ScaffoldedDomains() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDomainsDiff(t *testing.T) {
	tests := []struct {
		name         string
		have, wanted []string
		added        []string
		removed      []string
	}{
		{"agree", []string{"webshop"}, []string{"webshop"}, nil, nil},
		{"both empty", nil, nil, nil, nil},
		{"added after the scaffold", []string{"webshop"}, []string{"billing", "webshop"}, []string{"billing"}, nil},
		{"removed after the scaffold", []string{"billing", "webshop"}, []string{"webshop"}, nil, []string{"billing"}},
		{"order and repeats don't matter", []string{"webshop", "billing"}, []string{"billing", "billing", "stock"},
			[]string{"stock"}, []string{"webshop"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			added, removed := DomainsDiff(tt.have, tt.wanted)
			if !slices.Equal(added, tt.added) || !slices.Equal(removed, tt.removed) {
				t.Errorf("DomainsDiff() = %v, %v; want %v, %v", added, removed, tt.added, tt.removed)
			}
		})
	}
}
