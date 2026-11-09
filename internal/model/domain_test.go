package model

import (
	"slices"
	"testing"
)

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

// #42: an adoption renders the Standard with the Domains the repository was
// scaffolded with, failing that the system's, failing that none.
func TestAdoptionDomains(t *testing.T) {
	catalog := []string{"church-ops", "webshop", "postgres-docs"}
	isDomain := func(name string) bool { return slices.Contains(catalog, name) }
	tests := []struct {
		name           string
		scaffoldedFrom []string
		system         []string
		want           [][]string
	}{
		{name: "never scaffolded, no Domains", want: [][]string{nil}},
		{name: "never scaffolded, the system names Domains", system: []string{"webshop"},
			want: [][]string{{"webshop"}, nil}},
		{name: "scaffolded with a Domain the system still names",
			scaffoldedFrom: []string{"python-fastapi@v1.3.0", "python-standard@v1.7.0", "postgres@v1.1.0", "church-ops@v0.1.0"},
			system:         []string{"church-ops"},
			want:           [][]string{{"church-ops"}, nil}},
		{name: "the system names one more since the scaffold",
			scaffoldedFrom: []string{"python-standard@v1.7.0", "church-ops@v0.1.0"},
			system:         []string{"webshop", "church-ops"},
			want:           [][]string{{"church-ops"}, {"church-ops", "webshop"}, nil}},
		{name: "scaffolded with Domains, sorted and unique",
			scaffoldedFrom: []string{"webshop@v1.0.0", "church-ops@v0.1.0", "webshop@v1.0.0"},
			want:           [][]string{{"church-ops", "webshop"}, nil}},
		{name: "entries no Domain is are left out",
			scaffoldedFrom: []string{"python-fastapi@v1.3.0", "postgres@v1.1.0"},
			want:           [][]string{nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AdoptionDomains(tt.scaffoldedFrom, isDomain, tt.system)
			if !slices.EqualFunc(got, tt.want, slices.Equal[[]string]) {
				t.Errorf("AdoptionDomains() = %q, want %q", got, tt.want)
			}
		})
	}
}
