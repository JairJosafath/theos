/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
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
