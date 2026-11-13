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
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
)

// #73: a template's package.json and package-lock.json name the license the
// repository is scaffolded with, the one whose text is its LICENSE, and
// UNLICENSED, npm's word for "no license granted", when none is set. They
// said UNLICENSED whatever the Project chose, beside an MIT LICENSE.
func TestNodeManifestsNameTheScaffoldsLicense(t *testing.T) {
	withManifest, err := filepath.Glob(filepath.Join(templatesDir, "*", "package.json.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	want := make([]string, 0, len(withManifest))
	for _, p := range withManifest {
		want = append(want, filepath.Base(filepath.Dir(p)))
	}
	if len(want) == 0 {
		t.Fatal("no template has a package.json.tmpl")
	}

	licenses := []struct {
		name string
		lic  License
		id   string
	}{
		{"none set", License{}, "UNLICENSED"},
		{"MIT", License{ID: "MIT", Holder: "Webshop", Year: 2026}, "MIT"},
		{"Apache-2.0", License{ID: "Apache-2.0", Holder: "Webshop", Year: 2026}, "Apache-2.0"},
	}
	var covered []string
	for _, combo := range templateCombos {
		in, layers := loadCombo(t, combo.typ, combo.layers, "")
		for _, l := range licenses {
			in.License = l.lic
			result, err := Build(in, layers, Options{})
			if err != nil {
				t.Fatalf("%s, %s: %v", combo.name, l.name, err)
			}
			manifest, ok := result.Files["package.json"]
			if !ok {
				break // no Node package in this combination
			}
			if !slices.Contains(covered, in.Framework) {
				covered = append(covered, in.Framework)
			}
			t.Run(combo.name+"/"+l.name, func(t *testing.T) {
				var pkg struct {
					License string `json:"license"`
				}
				if err := json.Unmarshal(manifest.Data, &pkg); err != nil {
					t.Fatalf("package.json: %v", err)
				}
				if pkg.License != l.id {
					t.Errorf("package.json says license %q, want %q", pkg.License, l.id)
				}
				var lock struct {
					Packages map[string]struct {
						License string `json:"license"`
					} `json:"packages"`
				}
				if err := json.Unmarshal(result.Files["package-lock.json"].Data, &lock); err != nil {
					t.Fatalf("package-lock.json: %v", err)
				}
				if got := lock.Packages[""].License; got != l.id {
					t.Errorf("package-lock.json's root package says license %q, want %q as package.json", got, l.id)
				}
				// The manifest and the LICENSE file say the same.
				_, hasText := result.Files[LicensePath]
				if hasText != (l.lic.ID != "") {
					t.Errorf("LICENSE written: %v, with license %q", hasText, l.lic.ID)
				}
			})
		}
	}
	slices.Sort(covered)
	slices.Sort(want)
	if !slices.Equal(covered, want) {
		t.Errorf("combos rendering a package.json cover the Frameworks %v; the templates with a package.json.tmpl are %v",
			covered, want)
	}
}
