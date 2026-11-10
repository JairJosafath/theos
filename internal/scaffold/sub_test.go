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
