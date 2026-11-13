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
	"slices"
	"strings"
	"testing"
)

// ts-standard's .prettierignore lists docs/agents/, the folder the factory
// writes and agents never edit: the Standard's own guides and every Domain's
// Markdown. Prettier checking it failed make check on a fresh scaffold with a
// Domain, at fmt-check, before anyone changed a line (#38).
func TestTSStandardPrettierSkipsDocsAgents(t *testing.T) {
	typ, sides := standardCombo(t, "ts-standard")
	in, layers := loadCombo(t, typ, sides, "")
	out, err := Build(in, layers, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ignore, ok := out.Files[".prettierignore"]
	if !ok {
		t.Fatal("ts-standard renders no .prettierignore")
	}
	if !slices.Contains(strings.Split(string(ignore.Data), "\n"), "docs/agents/") {
		t.Errorf(".prettierignore does not list docs/agents/:\n%s", ignore.Data)
	}
}
