package model

import (
	"regexp"
	"testing"
	"time"
)

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
