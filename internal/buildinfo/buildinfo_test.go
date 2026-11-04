/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package buildinfo

import (
	"os/exec"
	"strings"
	"testing"
)

func TestVersionIsDevUnlessStamped(t *testing.T) {
	if Version != DevVersion {
		t.Errorf("Version = %q in a build that set nothing; want %q", Version, DevVersion)
	}
}

// TestLdflagsStampVersion builds a program with the flag the operator's
// Dockerfile passes and checks it reports the stamped version.
func TestLdflagsStampVersion(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary")
	}
	out, err := exec.Command("go", "run",
		"-ldflags=-X=github.com/JairJosafath/theos/internal/buildinfo.Version=1.2.3",
		"./testdata/printversion").CombinedOutput()
	if err != nil {
		t.Fatalf("go run: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "1.2.3" {
		t.Errorf("stamped build reports %q; want 1.2.3", got)
	}
}
