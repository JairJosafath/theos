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
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/model"
)

// #83: a scaffolded operator's Go files and its hack/boilerplate.go.txt, the
// header kubebuilder and controller-gen write into every file they add later,
// say what the repository's LICENSE says. They granted the Apache license
// whatever the Project chose, and with no license at all, where the factory
// picks none.
func TestOperatorHeadersNameTheScaffoldsLicense(t *testing.T) {
	const (
		boilerplate = "hack/boilerplate.go.txt"
		apache      = "Licensed under the Apache License, Version 2.0"
		spdx        = "SPDX-License-Identifier: "
	)
	cases := []struct {
		name string
		lic  License
		// grant is what every header says after its copyright line, "" for
		// the copyright line alone.
		grant string
	}{
		{"none set", License{}, ""},
		{"Apache-2.0", License{ID: "Apache-2.0", Holder: "Webshop", Year: 2026}, apache},
		{"MIT", License{ID: "MIT", Holder: "Webshop", Year: 2026}, spdx + "MIT"},
		{"GPL-3.0-only", License{ID: "GPL-3.0-only", Holder: "Webshop", Year: 2026}, spdx + "GPL-3.0-only"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, layers := loadCombo(t, "operator", []side{
				{model.SideFramework, "go-kubebuilder"},
				{model.SideStandard, "go-operator-standard"},
				{model.SideCompute, "small"},
				{model.SideDeployment, "gitea-release"},
			}, "")
			in.License = c.lic
			result, err := Build(in, layers, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := result.Files[boilerplate]; !ok {
				t.Fatalf("no %s; files: %v", boilerplate, result.Files.Paths())
			}
			headers := 0
			for _, p := range result.Files.Paths() {
				if p != boilerplate && !strings.HasSuffix(p, ".go") {
					continue
				}
				src := string(result.Files[p].Data)
				// The e2e files have their build tags above the header.
				_, rest, found := strings.Cut(src, "/*\nCopyright ")
				header, _, closed := strings.Cut("/*\nCopyright "+rest, "*/")
				if !found || !closed {
					t.Errorf("%s has no copyright header:\n%.80s", p, src)
					continue
				}
				headers++
				want := "/*\nCopyright 2026.\n"
				if p == boilerplate {
					// kubebuilder and controller-gen fill the year in.
					want = "/*\nCopyright YEAR.\n"
				}
				if c.grant != "" {
					want += "\n" + c.grant
				}
				if !strings.HasPrefix(header, want) {
					t.Errorf("%s: header\n%s\nwant it to start\n%s", p, header, want)
				}
				if c.grant == "" && header != want {
					t.Errorf("%s: header\n%s\nwant the copyright line alone", p, header)
				}
				if c.grant != apache && strings.Contains(src, "Apache License") {
					t.Errorf("%s names the Apache license with license %q", p, c.lic.ID)
				}
				if strings.HasSuffix(p, ".go") {
					if _, err := parser.ParseFile(token.NewFileSet(), p, src, parser.PackageClauseOnly|parser.ParseComments); err != nil {
						t.Errorf("%s does not parse: %v", p, err)
					}
				}
			}
			if headers < 5 {
				t.Errorf("%d files with a header, want %s and the Framework's Go files", headers, boilerplate)
			}
			_, hasText := result.Files[LicensePath]
			if hasText != (c.lic.ID != "") {
				t.Errorf("LICENSE written: %v, with license %q", hasText, c.lic.ID)
			}
		})
	}
}
