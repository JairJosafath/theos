/*
Copyright 2026.

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
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// typeIdentifier matches a named Container type from the API or the model.
var typeIdentifier = regexp.MustCompile(`\b(ContainerType|model\.Type)(StaticSite|Service|Job|Worker|MobileApp|CLI|Extension)\b`)

// TestOperatorIsTypeNeutral keeps everything type-specific in the catalog: the
// operator and the scaffold never branch on a Container type, so a new type is an
// enum value plus templates. See "Types" in the agent factory design.
func TestOperatorIsTypeNeutral(t *testing.T) {
	for _, dir := range []string{".", "../scaffold", "../agentrunner"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(data), "\n") {
				if m := typeIdentifier.FindString(line); m != "" {
					t.Errorf("%s:%d names the Container type %s; keep type-specific behaviour in the catalog", file, i+1, m)
				}
			}
		}
	}
}
