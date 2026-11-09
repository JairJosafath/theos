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

package chart

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestGuideInstallsTheChartVersion holds docs/getting-started.md to the
// release the chart names: a tag's guide installs that tag's release (#370).
// Release refuses a tag other than the chart's version, so a bump that misses
// the guide fails here first.
func TestGuideInstallsTheChartVersion(t *testing.T) {
	root := filepath.Join("..", "..")
	chart, err := os.ReadFile(filepath.Join(root, "dist", "chart", "Chart.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^version: *(\S+)$`).FindSubmatch(chart)
	if m == nil {
		t.Fatal("dist/chart/Chart.yaml has no version")
	}
	version := string(m[1])

	guide, err := os.ReadFile(filepath.Join(root, "docs", "getting-started.md"))
	if err != nil {
		t.Fatal(err)
	}
	sets := regexp.MustCompile(`(?m)^VERSION=(\S+)$`).FindAllSubmatch(guide, -1)
	if len(sets) == 0 {
		t.Fatal("docs/getting-started.md sets no VERSION")
	}
	for _, s := range sets {
		if string(s[1]) != version {
			t.Errorf("docs/getting-started.md sets VERSION=%s; the chart is %s", s[1], version)
		}
	}
	says := regexp.MustCompile("The commands use the release `([^`]+)`").FindSubmatch(guide)
	if says == nil {
		t.Fatal("docs/getting-started.md no longer says which release its commands use")
	}
	if string(says[1]) != version {
		t.Errorf("docs/getting-started.md says its commands use the release %s; the chart is %s", says[1], version)
	}
}
