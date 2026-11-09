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
	"bufio"
	"fmt"
	"path"
	"strings"
	"testing"
)

// crossBuildPending names the templates whose Dockerfile still RUNs on the
// target platform, and the issue that moves it to $BUILDPLATFORM. Empty
// since #1458 moved go-kubebuilder's.
var crossBuildPending = map[string]string{}

// TestDockerfilesRunOnlyOnTheBuildPlatform checks each Dockerfile a template
// renders, in each combination of templateCombos (so each Framework
// k8s-manifests writes one for): the image runner builds linux/amd64 and
// linux/arm64 with no emulation, so a RUN in a stage not built on
// $BUILDPLATFORM would fail the release (docs/plans/images.md, decision 3).
func TestDockerfilesRunOnlyOnTheBuildPlatform(t *testing.T) {
	seen := 0
	for _, c := range templateCombos {
		layers, result := renderCombo(t, c.typ, c.layers)
		for _, p := range result.Files.Paths() {
			if path.Base(p) != "Dockerfile" {
				continue
			}
			owner := ownerOf(layers, p)
			if issue, ok := crossBuildPending[owner]; ok {
				t.Logf("%s: %s's %s waits on %s", c.name, owner, p, issue)
				continue
			}
			seen++
			for _, err := range runsOnTargetPlatform(string(result.Files[p].Data)) {
				t.Errorf("%s: %s's %s: %v", c.name, owner, p, err)
			}
		}
	}
	if seen == 0 {
		t.Fatal("no template rendered a Dockerfile")
	}
}

func TestRunsOnTargetPlatform(t *testing.T) {
	cases := []struct {
		name, dockerfile string
		want             []string // the errors, in order
	}{
		{"build stage runs, target copies", `
FROM --platform=$BUILDPLATFORM golang:1 AS build
RUN go build -o /app .
FROM gcr.io/distroless/static
COPY --from=build /app /app
`, nil},
		{"braced, lower case, and a stage from a build stage", `
from --platform=${BUILDPLATFORM} node:24 as deps
run npm ci
FROM deps AS build
RUN npm run build
FROM nginx
COPY --from=build /dist /usr/share/nginx/html
`, nil},
		{"RUN on the target platform", `
FROM --platform=$BUILDPLATFORM python:3 AS build
RUN uv sync
FROM python:3
RUN apt-get update
`, []string{`line 5: RUN in stage 2 (FROM python:3), not built on $BUILDPLATFORM`}},
		{"no platform at all", `# syntax=docker/dockerfile:1
ARG IMAGE=alpine
FROM ${IMAGE} AS build
RUN echo hi \
  && echo there
`, []string{`line 4: RUN in stage 1 (FROM ${IMAGE} AS build), not built on $BUILDPLATFORM`}},
		{"a stage from a target stage", `
FROM alpine AS base
FROM base
RUN true
`, []string{`line 4: RUN in stage 2 (FROM base), not built on $BUILDPLATFORM`}},
		{"a fixed platform", `
FROM --platform=linux/amd64 alpine
RUN true
`, []string{`line 3: RUN in stage 1 (FROM --platform=linux/amd64 alpine), not built on $BUILDPLATFORM`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got []string
			for _, err := range runsOnTargetPlatform(c.dockerfile) {
				got = append(got, err.Error())
			}
			if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("errors:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(c.want, "\n"))
			}
		})
	}
}

// runsOnTargetPlatform returns an error for each RUN in a stage that isn't
// built on $BUILDPLATFORM: one FROM --platform=$BUILDPLATFORM, or FROM a
// stage that is.
func runsOnTargetPlatform(dockerfile string) []error {
	var errs []error
	onBuild := map[string]bool{} // stage name → built on $BUILDPLATFORM
	stage, from, build := 0, "", false
	scanner := bufio.NewScanner(strings.NewReader(dockerfile))
	line, start, instruction := 0, 0, ""
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if instruction == "" && (text == "" || strings.HasPrefix(text, "#")) {
			continue
		}
		if instruction == "" {
			start = line
		}
		if cont, ok := strings.CutSuffix(text, `\`); ok {
			instruction += cont + " "
			continue
		}
		instruction += text
		fields := strings.Fields(instruction)
		switch strings.ToUpper(fields[0]) {
		case "FROM":
			stage++
			from, build = instruction, false
			args := fields[1:]
			for len(args) > 0 && strings.HasPrefix(args[0], "--") {
				if p, ok := strings.CutPrefix(args[0], "--platform="); ok {
					build = p == "$BUILDPLATFORM" || p == "${BUILDPLATFORM}"
				}
				args = args[1:]
			}
			if len(args) > 0 && onBuild[strings.ToLower(args[0])] {
				build = true
			}
			if len(args) == 3 && strings.EqualFold(args[1], "AS") {
				onBuild[strings.ToLower(args[2])] = build
			}
		case "RUN":
			if !build {
				errs = append(errs, fmt.Errorf("line %d: RUN in stage %d (%s), not built on $BUILDPLATFORM", start, stage, from))
			}
		}
		instruction = ""
	}
	return errs
}
