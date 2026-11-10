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

package model

import (
	"reflect"
	"testing"
)

func TestC4ContextText(t *testing.T) {
	c := C4Context{
		System:    C4Element{Name: "tools", DisplayName: "Tools", Description: "CEL support in Kubernetes YAML."},
		Needs:     []string{"Works offline"},
		KPIs:      []string{"Errors caught before apply"},
		Users:     []C4Element{{Name: "policy-author", DisplayName: "Policy author", Description: "Writes policies."}},
		Container: C4Element{Name: "cel-language-server", DisplayName: "CEL language server", Type: "cli", Description: "Checks CEL."},
		Links: []C4Link{
			{Other: C4Element{Kind: "Container", Name: "vscode-extension", DisplayName: "Tools for VS Code",
				Type: "extension"}, Description: "Starts it with --stdio", RepositoryURL: "http://gitea/tools/vscode-extension"},
			{Outgoing: true, Other: C4Element{Kind: "DataStore", Name: "cache"}},
		},
	}
	want := `System: Tools — CEL support in Kubernetes YAML.
Need: Works offline
KPI: Errors caught before apply
Used by: Policy author — Writes policies.
Application: CEL language server (cli) — Checks CEL.
Relationships:
- Tools for VS Code (Container vscode-extension, extension) uses this application: Starts it with --stdio. Repository: http://gitea/tools/vscode-extension
- This application uses cache (DataStore cache).`
	if got := c.Text(); got != want {
		t.Errorf("Text() =\n%s\nwant\n%s", got, want)
	}
	if got, want := c.RelatedRepositories(), map[string]string{"vscode-extension": "http://gitea/tools/vscode-extension"}; !reflect.DeepEqual(got, want) {
		t.Errorf("RelatedRepositories() = %v, want %v", got, want)
	}
}

func TestC4ContextTextMinimal(t *testing.T) {
	c := C4Context{Container: C4Element{Name: "app", Type: "service"}}
	if got, want := c.Text(), "Application: app (service)"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
}
