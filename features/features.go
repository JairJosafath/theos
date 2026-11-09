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

// Package features holds the devcontainer features theos ships. They are
// embedded so the operator can write them into the repositories it scaffolds.
package features

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/JairJosafath/theos/internal/scaffold"
)

//go:embed src/theos-session
var files embed.FS

// TheosSession returns the files of the theos-session feature, with paths relative to
// the feature's folder. Shell scripts are executable.
func TheosSession() (scaffold.Tree, error) {
	tree := scaffold.Tree{}
	root := "src/theos-session"
	err := fs.WalkDir(files, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, root+"/")
		tree[rel] = scaffold.File{Data: data, Executable: strings.HasSuffix(rel, ".sh")}
		return nil
	})
	return tree, err
}

// Session feature modes for ScaffoldOptions.
const (
	// SessionFeatureLocal writes the feature into each repository.
	SessionFeatureLocal = "local"
	// SessionFeatureNone leaves the Session client out of scaffolded devcontainers.
	SessionFeatureNone = "none"
)

// localFeatureDir is where the feature goes, under .devcontainer/.
const localFeatureDir = "theos-session"

// ScaffoldOptions returns how the operator adds the Session client to scaffolded
// devcontainers. mode is SessionFeatureLocal, SessionFeatureNone, or a feature
// reference such as an OCI image, used as is. binaryURL becomes the feature's
// binaryUrl option.
func ScaffoldOptions(mode, binaryURL string) (scaffold.Options, error) {
	settings := map[string]any{}
	if binaryURL != "" {
		settings["binaryUrl"] = binaryURL
	}
	switch mode {
	case SessionFeatureNone:
		return scaffold.Options{}, nil
	case SessionFeatureLocal, "":
		tree, err := TheosSession()
		if err != nil {
			return scaffold.Options{}, err
		}
		files := scaffold.Tree{}
		for p, f := range tree {
			files[localFeatureDir+"/"+p] = f
		}
		return scaffold.Options{
			Features:          map[string]any{"./" + localFeatureDir: settings},
			DevcontainerFiles: files,
		}, nil
	default:
		return scaffold.Options{Features: map[string]any{mode: settings}}, nil
	}
}
