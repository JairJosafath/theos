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
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// testEnvDownloadFlag finds the operator's download addresses for a remote
// TestEnvironment's machines in rendered manifests: theos-env, and the CI
// runner one that runs a CI job starts.
var testEnvDownloadFlag = regexp.MustCompile(`(?m)^\s*- --test-environment-(tools|runner)-url=(\S+)$`)

// TestReleaseAttachesWhatRemoteMachinesDownload packages the chart as
// Release does and renders it: by default a remote TestEnvironment's
// machines download theos-env, and one that runs a CI job the CI runner too,
// from the release's files, each checked against the SHA256SUMS next to it
// (docs/plans/done/test-environments.md, steps 7 to 9). Release attaches each
// build of each, theos-env's for Linux and macOS, and lists them in
// SHA256SUMS.
func TestReleaseAttachesWhatRemoteMachinesDownload(t *testing.T) {
	helm := helmBinary(t)
	const version = "1.2.3"
	dir := t.TempDir()
	if out, err := exec.Command(helm, "package", chartDir, "--version", version, "--app-version", version,
		"--destination", dir).CombinedOutput(); err != nil {
		t.Fatalf("helm package: %v\n%s", err, out)
	}
	out, err := exec.Command(helm, "template", release, filepath.Join(dir, "theos-"+version+".tgz"),
		"--namespace", releaseNS, "--show-only", "templates/manager/manager.yaml").Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}
	releaseFiles := "https://github.com/JairJosafath/theos/releases/download/v" + version + "/"
	found := map[string]string{}
	for _, m := range testEnvDownloadFlag.FindAllSubmatch(out, -1) {
		found[string(m[1])] = string(m[2])
	}
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	create := regexp.MustCompile(`(?m)^\s*gh release create (?:.*\\\n)*.*$`).Find(workflow)
	sums := regexp.MustCompile(`(?m)^\s*sha256sum (.*) >SHA256SUMS$`).FindSubmatch(workflow)
	for _, what := range []string{"tools", "runner"} {
		url, ok := found[what]
		if !ok || !strings.HasPrefix(url, releaseFiles) || !strings.Contains(url, "${arch}") {
			t.Errorf("the chart's --test-environment-%s-url is %q, want one of the release's files, by ${arch}", what, url)
			continue
		}
		// theos-env's address names ${os} too: an EC2 Mac's is darwin's
		// (step 9).
		systems := []string{""}
		if strings.Contains(url, "${os}") {
			systems = []string{"linux", "darwin"}
		}
		for _, system := range systems {
			for _, arch := range []string{"amd64", "arm64"} {
				file := strings.NewReplacer("${os}", system, "${arch}", arch).Replace(strings.TrimPrefix(url, releaseFiles))
				if !strings.Contains(string(create), " "+file+" ") {
					t.Errorf("release.yml's gh release create doesn't attach %s: %s", file, create)
				}
				if sums == nil || !slices.Contains(strings.Fields(string(sums[1])), file) {
					t.Errorf("release.yml's SHA256SUMS doesn't cover %s (%q)", file, sums)
				}
			}
		}
	}

	out, err = exec.Command(helm, "template", release, chartDir, "--namespace", releaseNS,
		"--show-only", "templates/manager/manager.yaml", "--set", "testEnvironments.runnerURL=").Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}
	for _, m := range testEnvDownloadFlag.FindAllSubmatch(out, -1) {
		if string(m[1]) == "runner" {
			t.Errorf("an empty runnerURL still sets %s", m[0])
		}
	}
}
