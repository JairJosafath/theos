package model

import (
	"reflect"
	"strings"
	"testing"
)

func TestDecidePackages(t *testing.T) {
	npm := ReleasePackage{Outcome: PackagePublished, Ecosystem: "npm", Name: "@theos/orders-api",
		Version: "0.1.0", File: "theos-orders-api-0.1.0.tgz"}
	wheel := ReleasePackage{Outcome: PackagePublished, Ecosystem: "PyPI", Name: "orders-api", Version: "0.1.0",
		File: "orders_api-0.1.0-py3-none-any.whl"}
	exists := func(p ReleasePackage) ReleasePackage { p.Outcome = PackageExists; return p }
	unscoped := ReleasePackage{Outcome: PackageUnscoped, Ecosystem: "npm", Name: "orders-api", Version: "0.1.0",
		File: "orders-api-0.1.0.tgz", Reason: "orders-api is not under the scope @theos"}
	invalid := ReleasePackage{Outcome: PackageInvalid, Ecosystem: "npm", Name: "@theos/orders-api",
		Version: "0.2.0", File: "theos-orders-api-0.2.0.tgz", Reason: "version 0.2.0 is not the tag's, 0.1.0"}
	both := []PublishedPackage{{Ecosystem: "npm", Name: "@theos/orders-api", Version: "0.1.0"},
		{Ecosystem: "PyPI", Name: "orders-api", Version: "0.1.0"}}
	tests := []struct {
		name  string
		facts PackagesFacts
		state string
		done  bool
		// reason is the condition's; said are parts of its message; published
		// the packages listed.
		reason    string
		said      []string
		published []PublishedPackage
	}{
		{name: "the runs still run", facts: PackagesFacts{Tag: "v0.1.0", Run: RunRunning},
			state: PackagesStateWaiting, reason: PackagesReasonWaitsForRelease,
			said: []string{"Release runs of v0.1.0 are Running", "once they succeed"}},
		{name: "no run yet", facts: PackagesFacts{Tag: "v0.1.0", Run: RunWaiting},
			state: PackagesStateWaiting, reason: PackagesReasonWaitsForRelease,
			said: []string{"Release runs of v0.1.0 are Waiting"}},
		{name: "the host can't be reached", facts: PackagesFacts{Tag: "v0.1.0", Run: RunSucceeded,
			Failure: "connection refused"},
			state: PackagesStateFailed, reason: PackagesReasonGitFailure,
			said: []string{"Could not publish the packages of v0.1.0: connection refused"}},
		{name: "a tarball and a wheel are published", facts: PackagesFacts{Tag: "v0.1.0", Run: RunSucceeded,
			Packages: []ReleasePackage{npm, wheel}},
			state: PackagesStatePublished, done: true, reason: PackagesReasonPublished,
			said:      []string{"Published npm @theos/orders-api 0.1.0 and PyPI orders-api 0.1.0"},
			published: both},
		{name: "a repeat finds them there", facts: PackagesFacts{Tag: "v0.1.0", Run: RunSucceeded,
			Packages: []ReleasePackage{exists(npm), exists(wheel)}},
			state: PackagesStatePublished, done: true, reason: PackagesReasonPublished,
			said:      []string{"Published npm @theos/orders-api 0.1.0 and PyPI orders-api 0.1.0"},
			published: both},
		{name: "no package file", facts: PackagesFacts{Tag: "v0.1.0", Run: RunSucceeded},
			state: PackagesStateNoPackage, done: true, reason: PackagesReasonNoPackage,
			said: []string{"v0.1.0 attached no npm tarball, wheel or sdist; no package to publish"}},
		{name: "an unscoped tarball beside a wheel is left alone and named", facts: PackagesFacts{Tag: "v0.1.0",
			Run: RunSucceeded, Packages: []ReleasePackage{unscoped, wheel}},
			state: PackagesStatePublished, done: true, reason: PackagesReasonPublished,
			said: []string{"Published PyPI orders-api 0.1.0; left orders-api-0.1.0.tgz alone: " +
				"orders-api is not under the scope @theos"},
			published: both[1:]},
		{name: "an unscoped tarball alone is no package", facts: PackagesFacts{Tag: "v0.1.0", Run: RunSucceeded,
			Packages: []ReleasePackage{unscoped}},
			state: PackagesStateNoPackage, done: true, reason: PackagesReasonNoPackage,
			said: []string{"no package to publish; left orders-api-0.1.0.tgz alone: " +
				"orders-api is not under the scope @theos"}},
		{name: "a version that isn't the tag's fails the release, the wheel still listed",
			facts: PackagesFacts{Tag: "v0.1.0", Run: RunSucceeded, Packages: []ReleasePackage{invalid, wheel}},
			state: PackagesStateInvalid, reason: PackagesReasonInvalid,
			said:      []string{"Did not publish theos-orders-api-0.2.0.tgz: version 0.2.0 is not the tag's, 0.1.0"},
			published: both[1:]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := DecidePackages(tt.facts)
			if d.State != tt.state || d.Done != tt.done || d.Reason != tt.reason {
				t.Fatalf("got state %q done %v reason %q, want %q %v %q", d.State, d.Done, d.Reason,
					tt.state, tt.done, tt.reason)
			}
			for _, s := range tt.said {
				if !strings.Contains(d.Message, s) {
					t.Errorf("message %q lacks %q", d.Message, s)
				}
			}
			if !reflect.DeepEqual(d.Published, tt.published) {
				t.Errorf("published %v, want %v", d.Published, tt.published)
			}
		})
	}
}
