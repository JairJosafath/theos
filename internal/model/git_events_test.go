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
	"testing"
	"time"
)

// #1222: while deliveries arrive the polls that wait for the host stretch to
// EventsFallbackPoll, and never shorten.
func TestHostRecheck(t *testing.T) {
	tests := []struct {
		name   string
		d      time.Duration
		events bool
		want   time.Duration
	}{
		{"events off", 15 * time.Second, false, 15 * time.Second},
		{"events off, a longer poll", time.Hour, false, time.Hour},
		{"events on, a shorter poll", 15 * time.Second, true, EventsFallbackPoll},
		{"events on, a longer poll", time.Hour, true, time.Hour},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HostRecheck(tt.d, tt.events); got != tt.want {
				t.Errorf("HostRecheck(%s, %t) = %s, want %s", tt.d, tt.events, got, tt.want)
			}
		})
	}
}

func TestGitEventsURL(t *testing.T) {
	tests := []struct {
		base, namespace, name, want string
	}{
		{base: "http://theos-git-events.theos-system.svc:8082", namespace: "shop", name: "git",
			want: "http://theos-git-events.theos-system.svc:8082/git-events/shop/git"},
		{base: "https://factory.example/", namespace: "shop", name: "git",
			want: "https://factory.example/git-events/shop/git"},
		{base: "https://factory.example/theos", namespace: "a", name: "b",
			want: "https://factory.example/theos/git-events/a/b"},
	}
	for _, tt := range tests {
		if got := GitEventsURL(tt.base, tt.namespace, tt.name); got != tt.want {
			t.Errorf("GitEventsURL(%q, %q, %q) = %q, want %q", tt.base, tt.namespace, tt.name, got, tt.want)
		}
	}
}

func TestGitEventsSecretName(t *testing.T) {
	if got, want := GitEventsSecretName("git"), "git-git-events"; got != want {
		t.Errorf("GitEventsSecretName(git) = %q, want %q", got, want)
	}
}
