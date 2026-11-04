package model

import (
	"strings"
	"testing"
	"time"
)

func TestDecideModule(t *testing.T) {
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	engine := "github.com/JairJosafath/cel-lens/engine"
	// celLens is v0.15.1 of cel-lens-core, whose source goes to the engine
	// folder of JairJosafath/cel-lens, asked of proxy.golang.org.
	celLens := func(edit func(*ModuleFacts)) ModuleFacts {
		f := ModuleFacts{Path: engine, Version: "v0.15.1", Registry: RegistryUploaded,
			Repository: "JairJosafath/cel-lens", Source: &PublishSource{Path: "engine"},
			Proxy: "https://proxy.golang.org", Now: now}
		edit(&f)
		return f
	}
	tests := []struct {
		name  string
		facts ModuleFacts
		ask   bool
		state string
		ok    bool
		// reason is the condition's; said are parts of its message.
		reason string
		said   []string
	}{
		{name: "the host can't be reached", facts: ModuleFacts{Path: engine, Version: "v0.15.1",
			Registry: RegistryFailed, RegistryDetail: "connection refused"},
			state: ModuleStateFailed, reason: ModuleReasonGitFailure,
			said: []string{"Could not publish the Go module of v0.15.1: connection refused"}},
		{name: "Go refuses the version", facts: ModuleFacts{Path: engine, Version: "v2.0.0",
			Registry: RegistryInvalid, RegistryDetail: "module path lacks /v2"},
			state: ModuleStateInvalid, reason: ModuleReasonInvalid,
			said: []string{"Did not publish " + engine + " v2.0.0: module path lacks /v2"}},
		{name: "no go.mod", facts: ModuleFacts{Version: "v0.15.1", Registry: RegistryNoGoMod},
			state: ModuleStateNotGo, ok: true, reason: ModuleReasonNotGo, said: []string{"no go.mod"}},
		{name: "a factory path is published on upload", facts: ModuleFacts{Path: "theos.local/shop/api",
			Version: "v1.2.0", Registry: RegistryUploaded, Proxy: "https://proxy.golang.org", Now: now},
			state: ModuleStatePublished, ok: true, reason: ModuleReasonPublished,
			said: []string{"Published theos.local/shop/api v1.2.0"}},
		{name: "the issue's case: no source tag, Published refused",
			facts: celLens(func(f *ModuleFacts) {
				f.PublishedReason, f.PublishedMessage = "Unauthorized", "GitHub refused the token"
			}),
			state: ModuleStateWaiting, reason: ModuleReasonWaitsForRelease,
			said: []string{"Uploaded " + engine + " v0.15.1 to the registry", "no tag engine/v0.15.1 yet",
				"Published is Unauthorized, GitHub refused the token"}},
		{name: "own folder waits for the tag even with an old answer",
			facts: celLens(func(f *ModuleFacts) {
				f.SourceTag, f.Answer, f.Checked = "engine/v0.15.0", ProxyFound, now.Add(-time.Hour)
			}),
			state: ModuleStateWaiting, reason: ModuleReasonWaitsForRelease, said: []string{"no tag engine/v0.15.1 yet"}},
		{name: "own folder, tag out, never asked",
			facts: celLens(func(f *ModuleFacts) { f.SourceTag = "engine/v0.15.1" }), ask: true},
		{name: "an old Published with no checkedTime is asked again",
			facts: celLens(func(f *ModuleFacts) { f.SourceTag, f.Answer = "engine/v0.15.1", ProxyFound }), ask: true},
		{name: "found",
			facts: celLens(func(f *ModuleFacts) {
				f.SourceTag, f.Answer, f.Checked = "engine/v0.15.1", ProxyFound, now
			}),
			state: ModuleStatePublished, ok: true, reason: ModuleReasonPublished,
			said: []string{"Published " + engine + " v0.15.1; https://proxy.golang.org has it"}},
		{name: "found stays found",
			facts: celLens(func(f *ModuleFacts) {
				f.SourceTag, f.Answer, f.Checked = "engine/v0.15.1", ProxyFound, now.Add(-time.Hour)
			}),
			state: ModuleStatePublished, ok: true, reason: ModuleReasonPublished},
		{name: "missing, asked under 5 minutes ago",
			facts: celLens(func(f *ModuleFacts) {
				f.SourceTag, f.Answer, f.Checked = "engine/v0.15.1", ProxyMissing, now.Add(-4*time.Minute)
				f.AnswerDetail = "not found: unknown revision engine/v0.15.1"
			}),
			state: ModuleStateNotFetchable, reason: ModuleReasonNotFetchable,
			said: []string{"can't fetch it (not found: unknown revision engine/v0.15.1)",
				"go get looks for the tag engine/v0.15.1 in JairJosafath/cel-lens"}},
		{name: "missing, asked 5 minutes ago",
			facts: celLens(func(f *ModuleFacts) {
				f.SourceTag, f.Answer, f.Checked = "engine/v0.15.1", ProxyMissing, now.Add(-ModuleCheckEvery)
			}), ask: true},
		{name: "unreachable",
			facts: celLens(func(f *ModuleFacts) {
				f.SourceTag, f.Answer, f.Checked = "engine/v0.15.1", ProxyUnreachable, now.Add(-time.Minute)
				f.AnswerDetail = "dial tcp: i/o timeout"
			}),
			state: ModuleStateWaiting, reason: ModuleReasonProxyUnavailable,
			said: []string{"could not ask https://proxy.golang.org", "dial tcp: i/o timeout"}},
		{name: "unreachable is asked again",
			facts: celLens(func(f *ModuleFacts) {
				f.SourceTag, f.Answer, f.Checked = "engine/v0.15.1", ProxyUnreachable, now.Add(-6*time.Minute)
			}), ask: true},
		{name: "another path is asked at once",
			facts: celLens(func(f *ModuleFacts) { f.Path = "example.com/lib" }), ask: true},
		{name: "a path in another host the proxy can't fetch",
			facts: celLens(func(f *ModuleFacts) {
				f.Path, f.Answer, f.Checked, f.AnswerDetail = "example.com/lib", ProxyMissing, now, "not found"
			}),
			state: ModuleStateNotFetchable, reason: ModuleReasonNotFetchable,
			said: []string{"nothing in this Crew publishes to example.com"}},
		{name: "a path near the Crew's folder says what Go needs",
			facts: celLens(func(f *ModuleFacts) {
				f.Path, f.Answer, f.Checked = "github.com/JairJosafath/cel-lens/core", ProxyMissing, now
			}),
			state: ModuleStateNotFetchable, reason: ModuleReasonNotFetchable,
			said: []string{"go install from GitHub finds it only as github.com/JairJosafath/cel-lens/engine"}},
		{name: "a Crew with no GitHub target",
			facts: ModuleFacts{Path: engine, Version: "v0.15.1", Registry: RegistryUploaded,
				Proxy: "https://proxy.golang.org", Answer: ProxyFound, Checked: now, Now: now},
			state: ModuleStatePublished, ok: true, reason: ModuleReasonPublished},
		{name: "no proxy: the source tag is the answer",
			facts: celLens(func(f *ModuleFacts) { f.Proxy, f.SourceTag = "", "engine/v0.15.1" }),
			state: ModuleStatePublished, ok: true, reason: ModuleReasonPublished,
			said: []string{"tagged engine/v0.15.1 on GitHub; no Go proxy was asked"}},
		{name: "no proxy: own folder waits for the tag",
			facts: celLens(func(f *ModuleFacts) { f.Proxy = "" }),
			state: ModuleStateWaiting, reason: ModuleReasonWaitsForRelease},
		{name: "no proxy: another path can't be told",
			facts: celLens(func(f *ModuleFacts) { f.Proxy, f.Path = "", "example.com/lib" }),
			state: ModuleStateNotFetchable, reason: ModuleReasonNotFetchable,
			said: []string{"the factory asks no Go proxy, so the factory can't tell"}},
		{name: "private: the source tag is the answer",
			facts: celLens(func(f *ModuleFacts) { f.Private, f.SourceTag = true, "engine/v0.15.1" }),
			state: ModuleStatePublished, ok: true, reason: ModuleReasonPublished,
			said: []string{"JairJosafath/cel-lens is private, so fetch it with GOPRIVATE=" + engine}},
		{name: "private: another path can't be told",
			facts: celLens(func(f *ModuleFacts) { f.Private, f.Path = true, "example.com/lib" }),
			state: ModuleStateNotFetchable, reason: ModuleReasonNotFetchable,
			said: []string{"JairJosafath/cel-lens is private, so no public Go proxy reads it"}},
		{name: "a source tag Go doesn't read is no own folder, asked at once",
			facts: celLens(func(f *ModuleFacts) {
				prefix := "engine/release-"
				f.Source.TagPrefix = &prefix
			}),
			ask: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DecideModule(tt.facts)
			if got.Ask != tt.ask {
				t.Fatalf("Ask = %v, want %v (%+v)", got.Ask, tt.ask, got)
			}
			if tt.ask {
				if got != (ModuleDecision{Ask: true}) {
					t.Errorf("an ask says nothing else, got %+v", got)
				}
				return
			}
			if got.State != tt.state || got.Fetchable != tt.ok || got.Reason != tt.reason {
				t.Errorf("got %s %v %s, want %s %v %s: %s", got.State, got.Fetchable, got.Reason,
					tt.state, tt.ok, tt.reason, got.Message)
			}
			for _, s := range tt.said {
				if !strings.Contains(got.Message, s) {
					t.Errorf("message %q lacks %q", got.Message, s)
				}
			}
		})
	}
}

func TestDecideModuleAfterAsk(t *testing.T) {
	// The operator asks, then decides again with the answer and now.
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	f := ModuleFacts{Path: "example.com/lib", Version: "v1.0.0", Registry: RegistryUploaded,
		Proxy: "https://proxy.golang.org", Now: now}
	if !DecideModule(f).Ask {
		t.Fatal("never asked, want an ask")
	}
	f.Answer, f.AnswerDetail = ProxyAnswerOf(404, "not found: example.com/lib@v1.0.0: invalid version\n")
	f.Checked = now
	got := DecideModule(f)
	if got.Ask || got.Reason != ModuleReasonNotFetchable ||
		!strings.Contains(got.Message, "(not found: example.com/lib@v1.0.0: invalid version)") {
		t.Errorf("after a 404, got %+v", got)
	}
}

func TestProxyAnswerOf(t *testing.T) {
	tests := []struct {
		code   int
		body   string
		answer ProxyAnswer
		detail string
	}{
		{200, `{"Version":"v0.15.1"}`, ProxyFound, ""},
		{404, "not found: unknown revision engine/v0.15.1\nmore", ProxyMissing, "not found: unknown revision engine/v0.15.1"},
		{410, "gone", ProxyMissing, "gone"},
		{502, "bad gateway", ProxyUnreachable, "HTTP 502: bad gateway"},
		{500, "", ProxyUnreachable, "HTTP 500"},
		{404, strings.Repeat("x", 400), ProxyMissing, strings.Repeat("x", 300) + "..."},
	}
	for _, tt := range tests {
		answer, detail := ProxyAnswerOf(tt.code, tt.body)
		if answer != tt.answer || detail != tt.detail {
			t.Errorf("ProxyAnswerOf(%d, %q) = %s %q, want %s %q", tt.code, tt.body, answer, detail, tt.answer, tt.detail)
		}
	}
}

func TestModuleProxyURL(t *testing.T) {
	tests := []struct{ proxy, path, version, want string }{
		{"https://proxy.golang.org", "github.com/JairJosafath/cel-lens/engine", "v0.15.1",
			"https://proxy.golang.org/github.com/!jair!josafath/cel-lens/engine/@v/v0.15.1.info"},
		{"https://proxy.example/go/", "example.com/Lib", "v1.0.0-RC1",
			"https://proxy.example/go/example.com/!lib/@v/v1.0.0-!r!c1.info"},
	}
	for _, tt := range tests {
		got, err := ModuleProxyURL(tt.proxy, tt.path, tt.version)
		if err != nil || got != tt.want {
			t.Errorf("ModuleProxyURL(%q, %q) = %q, %v, want %q", tt.path, tt.version, got, err, tt.want)
		}
	}
	if _, err := ModuleProxyURL("https://proxy.golang.org", "bad path!", "v1.0.0"); err == nil {
		t.Error("a bad path, want an error")
	}
}
