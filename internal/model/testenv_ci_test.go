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
	"regexp"
	"strings"
	"testing"
	"time"
)

// A CI lease's holder names its runner on its GitProvider, and no Task's
// name reads as one (step 8).
func TestCIHolder(t *testing.T) {
	holder := CIHolder("git.example.com", 42)
	if holder != "ci/git.example.com/42" {
		t.Errorf("CIHolder = %q", holder)
	}
	provider, id, ok := ParseCIHolder(holder)
	if !ok || provider != "git.example.com" || id != 42 {
		t.Errorf("ParseCIHolder(%q) = %q, %d, %t", holder, provider, id, ok)
	}
	for _, notCI := range []string{"", "add-crop", "ci", "ci/", "ci/git", "ci//3", "ci/git/0", "ci/git/x", "ci/git/-1"} {
		if _, _, ok := ParseCIHolder(notCI); ok {
			t.Errorf("ParseCIHolder(%q) reads a CI holder", notCI)
		}
	}
}

// A CI lease's ID is a DNS label of the environment's, one per environment,
// provider, job and count, so leases and their runners never share a name.
func TestCILeaseID(t *testing.T) {
	label := regexp.MustCompile(`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`)
	longest := strings.Repeat("e", 32)
	id := CILeaseID(longest, "uid-1", "git", 7, 0)
	if !label.MatchString(id) || !strings.HasPrefix(id, longest+"-ci-") {
		t.Errorf("CILeaseID = %q, want a DNS label starting with the environment's name", id)
	}
	seen := map[string]bool{}
	for _, in := range []struct {
		uid, provider string
		job           int64
		k             int
	}{{"uid-1", "git", 7, 0}, {"uid-2", "git", 7, 0}, {"uid-1", "other", 7, 0}, {"uid-1", "git", 8, 0},
		{"uid-1", "git", 7, 1}} {
		id := CILeaseID("gpu", in.uid, in.provider, in.job, in.k)
		if seen[id] {
			t.Errorf("CILeaseID(%+v) = %q, which another lease has", in, id)
		}
		seen[id] = true
	}
	if first, again := CILeaseID("gpu", "uid-1", "git", 7, 0), CILeaseID("gpu", "uid-1", "git", 7, 0); first != again {
		t.Errorf("CILeaseID gave %q, then %q", first, again)
	}
}

// A CI lease is held while its runner runs its job, or waits to take one
// while one waits, or starts within the grace; it is returned once its job
// ended or nothing waits for it.
func TestCILeaseHeld(t *testing.T) {
	young, old := time.Minute, CIStartGrace+time.Minute
	for _, tt := range []struct {
		name    string
		runner  CIRunnerState
		waiting bool
		age     time.Duration
		want    bool
	}{
		{"its runner runs its job", CIRunnerState{Registered: true, Online: true, Busy: true}, false, old, true},
		{"its runner stopped mid-job, still busy", CIRunnerState{Registered: true, Busy: true}, false, old, true},
		{"its job ended, and the host removed its runner", CIRunnerState{}, true, young, false},
		{"its runner is online, and a job waits", CIRunnerState{Registered: true, Online: true}, true, old, true},
		{"its runner is online, and nothing waits", CIRunnerState{Registered: true, Online: true}, false, young, false},
		{"its machine starts, and a job waits", CIRunnerState{Registered: true}, true, young, true},
		{"its machine starts, and nothing waits", CIRunnerState{Registered: true}, false, young, false},
		{"its machine never started its runner", CIRunnerState{Registered: true}, true, old, false},
	} {
		if got := CILeaseHeld(tt.runner, tt.waiting, tt.age); got != tt.want {
			t.Errorf("%s: CILeaseHeld(%+v, %t, %v) = %t, want %t", tt.name, tt.runner, tt.waiting, tt.age, got,
				tt.want)
		}
	}
}

// A remote machine reaches the Git host only at a public https:// address
// on port 443; an in-cluster one, the chart's default, turns remote CI off.
func TestRemoteRunnerAddress(t *testing.T) {
	const inCluster = "http://git.git.svc.cluster.local:3000"
	for _, tt := range []struct {
		name, url, publicURL string
		want, why            string
	}{
		{"the chart's in-cluster host, no public address", inCluster, "", "", "inside the cluster"},
		{"a public address given", inCluster, "https://git.example.com/", "https://git.example.com", ""},
		{"a GitProvider on a public host", "https://git.example.com", "", "https://git.example.com", ""},
		{"port 443 named", inCluster, "https://git.example.com:443/sub", "https://git.example.com:443/sub", ""},
		{"another port", inCluster, "https://git.example.com:3000", "", "port 3000"},
		{"plain http", inCluster, "http://git.example.com", "", "not https://"},
		{"a public url on another port", "https://git.example.com:3000", "", "", "has no publicURL"},
		{"a service's short name", inCluster, "https://git", "", "inside the cluster"},
		{"a service's namespaced name", inCluster, "https://git.git.svc", "", "inside the cluster"},
		{"localhost", inCluster, "https://localhost", "", "inside the cluster"},
		{"a private address", inCluster, "https://10.0.0.5", "", "no public IPv4"},
		{"a loopback address", inCluster, "https://127.0.0.1", "", "no public IPv4"},
		{"an IPv6 address, which the fence lets out none of", inCluster, "https://[2001:db8::1]", "", "no public IPv4"},
		{"a public address by number", inCluster, "https://203.0.113.7", "https://203.0.113.7", ""},
	} {
		got, why := RemoteRunnerAddress(tt.url, tt.publicURL)
		if got != tt.want || (tt.why == "") != (why == "") || !strings.Contains(why, tt.why) {
			t.Errorf("%s: RemoteRunnerAddress(%q, %q) = %q, %q; want %q, why with %q", tt.name, tt.url, tt.publicURL,
				got, why, tt.want, tt.why)
		}
	}
}
