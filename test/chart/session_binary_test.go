/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
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

// sessionFlag finds the operator's --session-binary-url in rendered manifests.
var sessionFlag = regexp.MustCompile(`(?m)^\s*- --session-binary-url=(\S+)$`)

// TestReleasedChartNamesItsSessionClient packages the chart as Release does
// and renders it: the operator tells scaffolded devcontainers where to download
// theos-session. By default that is the UI, whose image carries the client under
// /downloads/, so a release of the private repository needs no GitHub login
// (#1151): the port-forward's localhost, or behind an Ingress the first
// ui.allowedHosts entry over ui.sessionBinaryScheme, https unless set
// (#1704). With the UI off it is the release's files, which Release attaches
// (#1079). A set sessionBinaryURL replaces the default;
// an empty one sets no flag. Both sources publish SHA256SUMS next to the
// binaries, which the feature verifies the download against (#1175).
func TestReleasedChartNamesItsSessionClient(t *testing.T) {
	helm := helmBinary(t)
	const version = "1.2.3"
	dir := t.TempDir()
	if out, err := exec.Command(helm, "package", chartDir, "--version", version, "--app-version", version,
		"--destination", dir).CombinedOutput(); err != nil {
		t.Fatalf("helm package: %v\n%s", err, out)
	}
	pkg := filepath.Join(dir, "theos-"+version+".tgz")
	manager := "templates/manager/manager.yaml"
	github := "https://github.com/JairJosafath/theos/releases/download/v" + version + "/theos-session-linux-${arch}"

	cases := []struct {
		name string
		set  []string
		want string
	}{
		{"default: the UI over port-forward", nil, "http://localhost:8080/downloads/theos-session-linux-${arch}"},
		{"the UI's port", []string{"ui.service.port=9090"}, "http://localhost:9090/downloads/theos-session-linux-${arch}"},
		{"behind an Ingress: the first allowed host", []string{"ui.allowedHosts={factory.example.com,other.example.com}"},
			"https://factory.example.com/downloads/theos-session-linux-${arch}"},
		{"#1704: an Ingress without TLS", []string{"ui.allowedHosts={factory.example.com}", "ui.sessionBinaryScheme=http"},
			"http://factory.example.com/downloads/theos-session-linux-${arch}"},
		{"the UI off: the release's files", []string{"ui.enabled=false"}, github},
		{"a set value replaces the default", []string{"sessionBinaryURL=https://x.example.com/${arch}"},
			"https://x.example.com/${arch}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := []string{"template", release, pkg, "--namespace", releaseNS, "--show-only", manager}
			for _, s := range c.set {
				args = append(args, "--set", s)
			}
			out, err := exec.Command(helm, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("helm template: %v\n%s", err, out)
			}
			m := sessionFlag.FindAllSubmatch(out, -1)
			if len(m) != 1 || string(m[0][1]) != c.want {
				t.Errorf("the released chart's --session-binary-url is %q; want once %s", m, c.want)
			}
		})
	}

	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// The whole command, its continued lines included.
	create := regexp.MustCompile(`(?m)^\s*gh release create (?:.*\\\n)*.*$`).Find(workflow)
	for _, arch := range []string{"amd64", "arm64"} {
		file := strings.ReplaceAll(filepath.Base(github), "${arch}", arch)
		if !strings.Contains(string(create), " "+file) {
			t.Errorf("release.yml's gh release create doesn't attach %s: %s", file, create)
		}
	}
	// Both places the chart's default downloads from serve a SHA256SUMS over
	// both binaries next to them, which the theos-session feature checks the
	// download against (#1175): the release's files and the UI image's
	// /downloads/ (ui/Dockerfile; ui/server.mjs serves it, its test shows).
	// A remote TestEnvironment's machine checks theos-env against the same
	// SHA256SUMS (docs/plans/test-environments.md, step 7), an EC2 Mac its
	// darwin build (step 9).
	if !strings.Contains(string(create), " SHA256SUMS") {
		t.Errorf("release.yml's gh release create doesn't attach SHA256SUMS: %s", create)
	}
	sums := regexp.MustCompile(`(?m)^\s*sha256sum (.*) >SHA256SUMS$`).FindSubmatch(workflow)
	for _, bin := range []string{"theos-session-linux-amd64", "theos-session-linux-arm64",
		"theos-env-linux-amd64", "theos-env-linux-arm64", "theos-env-darwin-amd64", "theos-env-darwin-arm64"} {
		if sums == nil || !slices.Contains(strings.Fields(string(sums[1])), bin) {
			t.Errorf("release.yml's SHA256SUMS doesn't cover %s (%q)", bin, sums)
		}
		if !strings.HasPrefix(bin, "theos-session-") && !strings.Contains(string(create), " "+bin) {
			t.Errorf("release.yml's gh release create doesn't attach %s: %s", bin, create)
		}
	}
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "ui", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	// The UI image serves the Session client alone.
	uiSums := "sha256sum theos-session-linux-amd64 theos-session-linux-arm64 >SHA256SUMS"
	if !strings.Contains(string(dockerfile), uiSums) {
		t.Errorf("ui/Dockerfile doesn't write SHA256SUMS next to the binaries it serves (%s)", uiSums)
	}

	out, err := exec.Command(helm, "template", release, pkg, "--namespace", releaseNS,
		"--show-only", manager, "--set", "sessionBinaryURL=").Output()
	if err != nil {
		t.Fatalf("helm template: %v", err)
	}
	if m := sessionFlag.FindSubmatch(out); m != nil {
		t.Errorf("an empty sessionBinaryURL still sets %s", m[0])
	}
}
