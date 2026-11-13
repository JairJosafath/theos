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

package model

import (
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Only the implementer's and verifier's runs lease; a spec writer, the
// reviewer, the triager and the planner never do (T3).
func TestLeasesEnvironments(t *testing.T) {
	for _, tt := range []struct {
		role string
		spec bool
		want bool
	}{
		{RoleImplementer, false, true},
		{RoleVerifier, false, true},
		{RoleImplementer, true, false},
		{RoleReviewer, false, false},
		{RoleTriager, false, false},
		{RolePlanner, false, false},
	} {
		if got := LeasesEnvironments(tt.role, tt.spec); got != tt.want {
			t.Errorf("LeasesEnvironments(%s, spec %v) = %v, want %v", tt.role, tt.spec, got, tt.want)
		}
	}
}

// A run gets each environment the project has of those its issue's labels
// and its Standard and Framework ask for; one its issue asks for that the
// project lacks makes it wait, one its Standard asks for is skipped (T2).
func TestEnvironmentsOf(t *testing.T) {
	required := EnvironmentsLabelled([]string{"status/ready", "env/browser", "env/android", "env/browser", "env/"})
	if !reflect.DeepEqual(required, []string{"android", "browser"}) {
		t.Fatalf("EnvironmentsLabelled = %v, want android and browser", required)
	}
	for _, tt := range []struct {
		name               string
		required, asked    []string
		project            []string
		get, missing, skip []string
	}{
		{name: "none asked", project: []string{"browser"}},
		{name: "the issue's, which the project has", required: []string{"browser"}, project: []string{"browser"},
			get: []string{"browser"}},
		{name: "the issue's, which the project lacks", required: []string{"android", "browser"},
			project: []string{"browser"}, get: []string{"browser"}, missing: []string{"android"}},
		{name: "the Standard's, which the project lacks", asked: []string{"godot"}, project: []string{"browser"},
			skip: []string{"godot"}},
		{name: "asked by both, once", required: []string{"browser"}, asked: []string{"browser", "godot"},
			project: []string{"browser", "godot"}, get: []string{"browser", "godot"}},
		{name: "the issue's wins over the Standard's", required: []string{"android"}, asked: []string{"android"},
			missing: []string{"android"}},
	} {
		got := EnvironmentsOf(tt.required, tt.asked, tt.project)
		want := RunEnvironments{Get: tt.get, Missing: tt.missing, Skipped: tt.skip}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: EnvironmentsOf = %+v, want %+v", tt.name, got, want)
		}
	}
}

// At maxLeases a run waits; past the day's budget no lease starts (T10).
func TestLeaseMayStart(t *testing.T) {
	for _, tt := range []struct {
		name          string
		live, max     int
		spent, budget int64
		want          string
	}{
		{"under the default limit", 1, 0, 0, 0, ""},
		{"at the default limit", 2, 0, 0, 0, EnvAllLeased},
		{"under its own limit", 3, 4, 0, 0, ""},
		{"no budget: local", 0, 2, 900_000, 0, ""},
		{"under the budget", 0, 2, 900, 1000, ""},
		{"at the budget", 0, 2, 1000, 1000, EnvOverBudget},
		{"past the budget and at the limit", 2, 2, 2000, 1000, EnvOverBudget},
	} {
		if got := LeaseMayStart(tt.live, tt.max, tt.spent, tt.budget); got != tt.want {
			t.Errorf("%s: LeaseMayStart = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// A waiting Task's message starts with why, which the Brief's Needs you
// reads (T2).
func TestEnvironmentWaitMessage(t *testing.T) {
	msg := EnvironmentWaitMessage(EnvNotInProject, []string{"android", "ios"}, "")
	if !strings.HasPrefix(msg, EnvNotInProject+": ") || !strings.Contains(msg, "`env/android` and `env/ios`") {
		t.Errorf("message %q, want it to start NotInProject and name the labels", msg)
	}
	msg = EnvironmentWaitMessage(EnvPending, []string{"browser"}, "the pod is starting")
	if !strings.HasPrefix(msg, EnvPending+": ") || !strings.HasSuffix(msg, ": the pod is starting") {
		t.Errorf("message %q, want Pending and the lease's detail", msg)
	}
}

func TestTestEnvVar(t *testing.T) {
	for _, tt := range []struct{ name, suffix, want string }{
		{"browser", TestEnvURL, "THEOS_ENV_BROWSER_URL"},
		{"android-34", TestEnvToken, "THEOS_ENV_ANDROID_34_TOKEN"},
		{"godot", TestEnvCA, "THEOS_ENV_GODOT_CA"},
	} {
		if got := TestEnvVar(tt.name, tt.suffix); got != tt.want {
			t.Errorf("TestEnvVar(%q, %q) = %q, want %q", tt.name, tt.suffix, got, tt.want)
		}
	}
}

// ImagePinned mirrors the TestEnvironment's CEL rule on local.image (T7).
func TestImagePinned(t *testing.T) {
	digest := "@sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tt := range []struct {
		image string
		want  bool
	}{
		{"ghcr.io/jairjosafath/theos/env-browser:0.31.0", true},
		{"mcr.microsoft.com/playwright:v1.64.1-noble", true},
		{"mcr.microsoft.com/playwright" + digest, true},
		{"mcr.microsoft.com/playwright:latest" + digest, true},
		{"localhost:5000/env-browser:dev", true},
		{"mcr.microsoft.com/playwright", false},
		{"mcr.microsoft.com/playwright:latest", false},
		{"localhost:5000/env-browser", false},
		{"env-browser@sha256:abc", false},
		{"", false},
	} {
		if got := ImagePinned(tt.image); got != tt.want {
			t.Errorf("ImagePinned(%q) = %v, want %v", tt.image, got, tt.want)
		}
	}
}

// A lease's ID is the same for the same run and environment, differs for
// another of either, and is a DNS label a pod can be named (T6, T7).
func TestLeaseID(t *testing.T) {
	label := regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)
	a := LeaseID("browser", "add-crop-r1-implement")
	if a != LeaseID("browser", "add-crop-r1-implement") {
		t.Error("LeaseID differs for the same run and environment")
	}
	for _, other := range []string{LeaseID("browser", "add-crop-r1-verify"), LeaseID("android", "add-crop-r1-implement")} {
		if other == a {
			t.Errorf("LeaseID(%q) is another run's or environment's", other)
		}
	}
	long := LeaseID("abcdefghijklmnopqrstuvwxyz012345", "a-very-long-task-name-that-goes-on-and-on-r12-implement-3-2")
	for _, id := range []string{a, long} {
		if !label.MatchString(id) {
			t.Errorf("LeaseID %q is not a DNS label", id)
		}
	}
}

func TestLeaseDeadline(t *testing.T) {
	if got := LeaseDeadline(0); got != 180*time.Minute {
		t.Errorf("LeaseDeadline(0) = %v, want the default 180m", got)
	}
	if got := LeaseDeadline(45); got != 45*time.Minute {
		t.Errorf("LeaseDeadline(45) = %v, want 45m", got)
	}
}

// A lease its holder doesn't list is returned once it is older than the
// grace, so one just taken and not yet recorded stays.
func TestOrphanLease(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name    string
		listed  bool
		started time.Time
		want    bool
	}{
		{"listed by its holder", true, now.Add(-time.Hour), false},
		{"unlisted and old", false, now.Add(-2 * time.Minute), true},
		{"unlisted and just taken", false, now.Add(-10 * time.Second), false},
		{"unlisted, start unknown", false, time.Time{}, false},
	} {
		if got := OrphanLease(tt.listed, tt.started, now, time.Minute); got != tt.want {
			t.Errorf("%s: OrphanLease = %v, want %v", tt.name, got, tt.want)
		}
	}
}
