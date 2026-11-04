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
