package scaffold

import (
	"reflect"
	"testing"
)

// TestTreeSub reads a template in a subdirectory of its repository (#35):
// only the files below the path, by their paths relative to it.
func TestTreeSub(t *testing.T) {
	repo := tree(
		"CLAUDE.md", "# church-ops",
		"model/system.yaml", "kind: SoftwareSystem",
		"domains/ops/README.md", "# Ops",
		"domains/ops/glossary/terms.md", "# Terms",
		"domains/opsx/README.md", "# Not ops",
	)
	tests := []struct {
		name   string
		dir    string
		want   Tree
		wantOK bool
	}{
		{name: "unset is the root", dir: "", want: repo, wantOK: true},
		{name: "a subdirectory, relative paths", dir: "domains/ops", wantOK: true,
			want: tree("README.md", "# Ops", "glossary/terms.md", "# Terms")},
		{name: "a nested directory", dir: "domains/ops/glossary", wantOK: true, want: tree("terms.md", "# Terms")},
		{name: "a missing directory", dir: "domains/billing", want: Tree{}},
		{name: "a file is no directory", dir: "CLAUDE.md", want: Tree{}},
		{name: "a prefix of a name is no directory", dir: "domains/op", want: Tree{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := repo.Sub(tt.dir)
			if ok != tt.wantOK || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Sub(%q) = %v, %v; want %v, %v", tt.dir, got.Paths(), ok, tt.want.Paths(), tt.wantOK)
			}
		})
	}
}
