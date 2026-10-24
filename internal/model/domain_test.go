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
