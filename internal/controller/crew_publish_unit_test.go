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

package controller

import (
	"testing"

	"github.com/JairJosafath/theos/internal/model"
)

// TestSourceName maps the names GitHub gives uploaded files back to Gitea's.
func TestSourceName(t *testing.T) {
	gitea := &model.SourceRelease{Assets: []model.ReleaseAsset{{Name: "cel lens.vsix"},
		{Name: "orders-cli_1.0+linux.tar.gz"}, {Name: "SHA256SUMS"}}}
	for name, want := range map[string]string{
		"cel.lens.vsix":               "cel lens.vsix",
		"orders-cli_1.0+linux.tar.gz": "orders-cli_1.0+linux.tar.gz",
		"SHA256SUMS":                  "SHA256SUMS",
		"other.txt":                   "other.txt",
	} {
		if got := sourceName(gitea, name); got != want {
			t.Errorf("sourceName(%q) = %q; want %q", name, got, want)
		}
	}
}
