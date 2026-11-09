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
	"os/exec"
	"regexp"
	"testing"
)

// checkpointClassFlag finds the operator's --sandbox-checkpoint-storage-class.
var checkpointClassFlag = regexp.MustCompile(`(?m)^\s*- --sandbox-checkpoint-storage-class=(\S+)$`)

// TestChartSetsTheCheckpointClass checks checkpointStorageClass (the review of
// step 11 of docs/plans/self-hosting.md): by default the chart sets no flag,
// so the checkpoint volume is of the cluster's default StorageClass; a set
// class, or none, which gives sandboxes no checkpoint volume, reaches the
// operator.
func TestChartSetsTheCheckpointClass(t *testing.T) {
	helm := helmBinary(t)
	for _, c := range []struct {
		name, set, want string
	}{
		{"default: the cluster's default class", "", ""},
		{"a class of the user's", "checkpointStorageClass=fast-ssd", "fast-ssd"},
		{"none: no checkpoint volume", "checkpointStorageClass=none", "none"},
	} {
		t.Run(c.name, func(t *testing.T) {
			args := []string{"template", release, chartDir, "--namespace", releaseNS,
				"--show-only", "templates/manager/manager.yaml"}
			if c.set != "" {
				args = append(args, "--set", c.set)
			}
			out, err := exec.Command(helm, args...).CombinedOutput()
			if err != nil {
				t.Fatalf("helm template: %v\n%s", err, out)
			}
			m := checkpointClassFlag.FindAllSubmatch(out, -1)
			switch {
			case c.want == "" && len(m) != 0:
				t.Errorf("the default chart sets %s; want no flag", m[0][0])
			case c.want != "" && (len(m) != 1 || string(m[0][1]) != c.want):
				t.Errorf("--sandbox-checkpoint-storage-class is %q; want once %s", m, c.want)
			}
		})
	}
}
