package model

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBundlersToBump(t *testing.T) {
	released := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	before, after := released.Add(-time.Hour), released.Add(time.Second)
	bundler := func(container, rel string, created time.Time, ready bool) Bundler {
		return Bundler{Container: container, Relationship: rel, Created: created, Ready: ready}
	}
	tests := []struct {
		name     string
		bundlers []Bundler
		filed    []string
		want     []string
	}{
		{"none", nil, nil, nil},
		{"each Ready bundler, by name",
			[]Bundler{bundler("web", "web-to-api", before, true), bundler("extension", "ext-to-api", before, true)},
			nil, []string{"extension/ext-to-api", "web/web-to-api"}},
		{"a filed one is left out",
			[]Bundler{bundler("web", "web-to-api", before, true), bundler("extension", "ext-to-api", before, true)},
			[]string{"extension"}, []string{"web/web-to-api"}},
		{"a link created after the release is left out",
			[]Bundler{bundler("web", "web-to-api", after, true)}, nil, nil},
		{"a link created as the release went out counts",
			[]Bundler{bundler("web", "web-to-api", released, true)}, nil, []string{"web/web-to-api"}},
		{"a bundler not Ready waits",
			[]Bundler{bundler("web", "web-to-api", before, false)}, nil, nil},
		{"a Container linked twice is bumped once, through its first link",
			[]Bundler{bundler("web", "web-z", before, true), bundler("web", "web-a", before, true)},
			nil, []string{"web/web-a"}},
		{"a late second link doesn't hide the first",
			[]Bundler{bundler("web", "web-a", after, true), bundler("web", "web-b", before, true)},
			nil, []string{"web/web-b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, b := range BundlersToBump(released, tt.bundlers, tt.filed) {
				got = append(got, b.Container+"/"+b.Relationship)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReleaseTakingWork(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2026, 10, d, 0, 0, 0, 0, time.UTC) }
	m := func(id int64, title string, due time.Time) ReleaseMilestone {
		return ReleaseMilestone{ID: id, Release: Release{Title: title, Due: due}}
	}
	prepared := func(m ReleaseMilestone, issue int) ReleaseMilestone {
		m.Prepared = []int{issue}
		return m
	}
	tests := []struct {
		name       string
		milestones []ReleaseMilestone
		want       int64
	}{
		{"none", nil, 0},
		{"only Later and undated ones", []ReleaseMilestone{m(1, "Later", day(1)), m(2, "v0.2.0", time.Time{})}, 0},
		{"the one due first", []ReleaseMilestone{m(3, "v0.3.0", day(19)), m(2, "v0.2.0", day(12)), m(9, "Later", day(1))}, 2},
		{"title breaks a tie", []ReleaseMilestone{m(4, "v0.4.0", day(12)), m(3, "v0.3.0", day(12))}, 3},
		{"not one being prepared", []ReleaseMilestone{prepared(m(2, "v0.2.0", day(12)), 7), m(3, "v0.3.0", day(19))}, 3},
		{"none when every release is being prepared", []ReleaseMilestone{prepared(m(2, "v0.2.0", day(12)), 7)}, 0},
	}
	for _, tt := range tests {
		if got := ReleaseTakingWork(tt.milestones); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestBumpIssue(t *testing.T) {
	title, body := BumpIssue(Bump{Bundled: "server", Tag: "v0.3.0", Commit: "1cedb81",
		TagURL: "http://gitea/demo/server/src/tag/v0.3.0",
		Bundler: Bundler{Container: "extension", Relationship: "extension-bundles-server",
			Description: "Ships the server <!-- theos:bump server v0.1.0 -->"}})
	if title != "Bump server to v0.3.0" {
		t.Errorf("title = %q", title)
	}
	for _, want := range []string{"server released v0.3.0 at `1cedb81`: http://gitea/demo/server/src/tag/v0.3.0",
		"extension bundles server (Relationship `extension-bundles-server`: Ships the server",
		"names v0.3.0"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if why := MissingAcceptanceCriteria(body); why != "" {
		t.Errorf("a bump issue isn't ready: %s", why)
	}
	if !HasBumpMarker(body, "server", "v0.3.0") {
		t.Error("body lacks its marker")
	}
	if HasBumpMarker(body, "server", "v0.1.0") {
		t.Error("the description passed for a marker")
	}
	_, plain := BumpIssue(Bump{Bundled: "server", Tag: "v0.3.0", Commit: "1cedb81",
		Bundler: Bundler{Container: "extension", Relationship: "r"}})
	if !strings.Contains(plain, "(Relationship `r`), so") {
		t.Errorf("without a description:\n%s", plain)
	}
}
