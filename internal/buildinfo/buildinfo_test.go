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
