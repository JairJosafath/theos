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

package cloudformation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The embedded bootstrap template is at least as new as every stack the
// fixtures' assemblies require (#1608).
func TestBootstrapTemplateCoversFixtures(t *testing.T) {
	body, version, err := bootstrapTemplate()
	if err != nil {
		t.Fatal(err)
	}
	if body == "" || !strings.Contains(body, "CdkBootstrapVersion") {
		t.Fatalf("embedded bootstrap template is empty or lacks CdkBootstrapVersion")
	}
	if !strings.Contains(body, "'"+strconv.Itoa(version)+"'") {
		t.Errorf("template body does not carry its version %d", version)
	}
	manifests, err := filepath.Glob("testdata/*/*manifest.json")
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no fixture manifests found: %v", err)
	}
	stacks := 0
	for _, path := range manifests {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			Artifacts map[string]struct {
				Type       string `json:"type"`
				Properties struct {
					Required *int `json:"requiresBootstrapStackVersion"`
				} `json:"properties"`
			} `json:"artifacts"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for name, a := range m.Artifacts {
			if a.Type != "aws:cloudformation:stack" || a.Properties.Required == nil {
				continue
			}
			stacks++
			if *a.Properties.Required > version {
				t.Errorf("%s: %s requires bootstrap version %d, embedded template is %d", path, name, *a.Properties.Required, version)
			}
		}
	}
	if stacks == 0 {
		t.Fatal("no stack artifact with requiresBootstrapStackVersion in the fixtures")
	}
}
