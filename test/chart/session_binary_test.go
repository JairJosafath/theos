/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package chart

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sessionFlag finds the operator's --session-binary-url in rendered manifests.
var sessionFlag = regexp.MustCompile(`(?m)^\s*- --session-binary-url=(\S+)$`)

// TestReleasedChartNamesItsSessionClient packages the chart as Release does
// and renders it: the operator tells scaffolded devcontainers to download
// c4session from that release's files, which Release attaches, so a release
// install's devcontainers register Sessions (#1079). An empty
// sessionBinaryURL sets no flag.
func TestReleasedChartNamesItsSessionClient(t *testing.T) {
	helm := helmBinary(t)
	const version = "1.2.3"
	dir := t.TempDir()
	if out, err := exec.Command(helm, "package", chartDir, "--version", version, "--app-version", version,
		"--destination", dir).CombinedOutput(); err != nil {
		t.Fatalf("helm package: %v\n%s", err, out)
	}
	pkg := filepath.Join(dir, "c4-factory-"+version+".tgz")
	manager := "templates/manager/manager.yaml"

	out, err := exec.Command(helm, "template", release, pkg, "--namespace", releaseNS,
		"--show-only", manager).Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}
	want := "https://github.com/JairJosafath/c4factory/releases/download/v" + version + "/c4session-linux-${arch}"
	m := sessionFlag.FindAllSubmatch(out, -1)
	if len(m) != 1 || string(m[0][1]) != want {
		t.Errorf("the released chart's --session-binary-url is %q; want once %s", m, want)
	}

	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	create := regexp.MustCompile(`(?m)^\s*gh release create .*$`).Find(workflow)
	for _, arch := range []string{"amd64", "arm64"} {
		file := strings.ReplaceAll(filepath.Base(want), "${arch}", arch)
		if !strings.Contains(string(create), " "+file) {
			t.Errorf("release.yml's gh release create doesn't attach %s: %s", file, create)
		}
	}

	out, err = exec.Command(helm, "template", release, pkg, "--namespace", releaseNS,
		"--show-only", manager, "--set", "sessionBinaryURL=").Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}
	if m := sessionFlag.FindSubmatch(out); m != nil {
		t.Errorf("an empty sessionBinaryURL still sets %s", m[0])
	}
}
