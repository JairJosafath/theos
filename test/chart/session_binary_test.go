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
// and renders it: the operator tells scaffolded devcontainers where to download
// c4session. By default that is the UI, whose image carries the client under
// /downloads/, so a release of the private repository needs no GitHub login
// (#1151): the port-forward's localhost, or behind an Ingress the first
// ui.allowedHosts entry over https. With the UI off it is the release's files,
// which Release attaches (#1079). A set sessionBinaryURL replaces the default;
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
	pkg := filepath.Join(dir, "c4-factory-"+version+".tgz")
	manager := "templates/manager/manager.yaml"
	github := "https://github.com/JairJosafath/c4factory/releases/download/v" + version + "/c4session-linux-${arch}"

	cases := []struct {
		name string
		set  []string
		want string
	}{
		{"default: the UI over port-forward", nil, "http://localhost:8080/downloads/c4session-linux-${arch}"},
		{"the UI's port", []string{"ui.service.port=9090"}, "http://localhost:9090/downloads/c4session-linux-${arch}"},
		{"behind an Ingress: the first allowed host", []string{"ui.allowedHosts={factory.example.com,other.example.com}"},
			"https://factory.example.com/downloads/c4session-linux-${arch}"},
		{"the UI off: the release's files", []string{"ui.enabled=false"}, github},
		{"a set value replaces the default", []string{"sessionBinaryURL=https://x.example.com/${arch}"}, "https://x.example.com/${arch}"},
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
	create := regexp.MustCompile(`(?m)^\s*gh release create .*$`).Find(workflow)
	for _, arch := range []string{"amd64", "arm64"} {
		file := strings.ReplaceAll(filepath.Base(github), "${arch}", arch)
		if !strings.Contains(string(create), " "+file) {
			t.Errorf("release.yml's gh release create doesn't attach %s: %s", file, create)
		}
	}
	// Both places the chart's default downloads from serve a SHA256SUMS over
	// both binaries next to them, which the c4session feature checks the
	// download against (#1175): the release's files and the UI image's
	// /downloads/ (ui/Dockerfile; ui/server.mjs serves it, its test shows).
	sums := "sha256sum c4session-linux-amd64 c4session-linux-arm64 >SHA256SUMS"
	if !strings.Contains(string(create), " SHA256SUMS") {
		t.Errorf("release.yml's gh release create doesn't attach SHA256SUMS: %s", create)
	}
	if !strings.Contains(string(workflow), sums) {
		t.Errorf("release.yml doesn't write SHA256SUMS over both binaries (%s)", sums)
	}
	dockerfile, err := os.ReadFile(filepath.Join("..", "..", "ui", "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), sums) {
		t.Errorf("ui/Dockerfile doesn't write SHA256SUMS next to the binaries it serves (%s)", sums)
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
