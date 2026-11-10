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

package modelproxy

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// The key files of the named upstreams in these tests.
const (
	openAIKeyFile = "OPENAI_API_KEY" //nolint:gosec // a key file's name, not a credential
	geminiKeyFile = "GEMINI_API_KEY" //nolint:gosec // a key file's name, not a credential
)

// clientKeys is every header a sandbox's client could send a key in.
func clientKeys() http.Header {
	return http.Header{"Authorization": {"Bearer " + placeholder}, "X-Api-Key": {clientKey},
		"X-Goog-Api-Key": {clientKey}, "Api-Key": {clientKey}}
}

// newNamedProxy serves a Proxy whose default upstream is defaultURL, with
// the named upstreams of specs, reading dir.
func newNamedProxy(t *testing.T, defaultURL, dir string, specs ...string) *httptest.Server {
	t.Helper()
	named := make([]Upstream, 0, len(specs))
	for _, spec := range specs {
		up, err := ParseUpstream(spec)
		if err != nil {
			t.Fatal(err)
		}
		named = append(named, up)
	}
	p, err := NewWithUpstreams(mustURL(t, defaultURL), named, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(p)
	t.Cleanup(proxy.Close)
	return proxy
}

// credentials is every credential header a request carries, as "Name: value".
func credentials(r *http.Request) []string {
	var got []string
	for _, name := range credentialHeaders {
		for _, v := range r.Header.Values(name) {
			got = append(got, name+": "+v)
		}
	}
	return got
}

// #58: a request under /upstreams/<name>/ goes to that upstream, on its own
// path, with that upstream's key the way it takes it, and with nothing the
// client sent as a credential. No upstream gets another's key.
func TestNamedUpstreamGetsItsOwnKeyAndNoOther(t *testing.T) {
	claudeToken, openAIKey, geminiKey := rand.Text(), rand.Text(), rand.Text()
	dir := t.TempDir()
	writeKey(t, dir, OAuthTokenKey, claudeToken, then)
	writeKey(t, dir, openAIKeyFile, openAIKey+"\n", then)
	writeKey(t, dir, geminiKeyFile, geminiKey, then)
	claude, openAI, gemini, local := newUpstream(t), newUpstream(t), newUpstream(t), newUpstream(t)
	proxy := newNamedProxy(t, claude.URL, dir,
		"openai="+openAI.URL+"/v1,key="+openAIKeyFile,
		"gemini="+gemini.URL+",auth=header:X-Goog-Api-Key,key="+geminiKeyFile,
		"local="+local.URL+"/v1")

	for _, tt := range []struct {
		name, path string
		up         *upstream
		wantPath   string
		want       []string
	}{
		{"a bearer key", "/upstreams/openai/responses?stream=true", openAI, "/v1/responses?stream=true",
			[]string{"Authorization: Bearer " + openAIKey}},
		{"a key in a header of the upstream's own", "/upstreams/gemini/v1beta/models/g:streamGenerateContent", gemini,
			"/v1beta/models/g:streamGenerateContent", []string{"X-Goog-Api-Key: " + geminiKey}},
		{"an upstream that takes no key", "/upstreams/local/chat/completions", local, "/v1/chat/completions", nil},
		{"an upstream's root", "/upstreams/local", local, "/v1/", nil},
		{"the default upstream, as before", "/v1/messages", claude, "/v1/messages",
			[]string{"Authorization: Bearer " + claudeToken}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if status, body := send(t, proxy, tt.path, clientKeys()); status != http.StatusOK {
				t.Fatalf("status = %d (%s); want 200", status, body)
			}
			got := tt.up.last(t)
			if got.URL.RequestURI() != tt.wantPath {
				t.Errorf("the upstream was asked for %s; want %s", got.URL.RequestURI(), tt.wantPath)
			}
			if creds := credentials(got); !slices.Equal(creds, tt.want) {
				t.Errorf("the upstream got the credentials %q; want %q", creds, tt.want)
			}
		})
	}
	// Each upstream saw its own requests only, and no other's key in any header.
	for name, u := range map[string]*upstream{"claude": claude, "openai": openAI, "gemini": gemini, "local": local} {
		others := map[string][]string{"claude": {openAIKey, geminiKey}, "openai": {claudeToken, geminiKey},
			"gemini": {claudeToken, openAIKey}, "local": {claudeToken, openAIKey, geminiKey}}[name]
		for _, r := range u.requests() {
			for header, values := range r.Header {
				for _, v := range values {
					for _, key := range append(others, clientKey, placeholder) {
						if strings.Contains(v, key) {
							t.Errorf("upstream %s got a key that isn't its own, in %s", name, header)
						}
					}
				}
			}
		}
	}
	if n := len(claude.requests()); n != 1 {
		t.Errorf("the default upstream got %d requests; want the 1 sent to it", n)
	}
}

// #58: a name no upstream has is refused, and forwards nothing: not to the
// default upstream either, which would get another vendor's request with
// Claude's key.
func TestUnconfiguredUpstreamIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeKey(t, dir, OAuthTokenKey, rand.Text(), then)
	claude, openAI := newUpstream(t), newUpstream(t)
	proxy := newNamedProxy(t, claude.URL, dir, "openai="+openAI.URL+",key="+openAIKeyFile)

	for _, path := range []string{"/upstreams/azure/responses", "/upstreams/", "/upstreams/openai-2/x",
		"/upstreams/../v1/messages", "/upstreams/OpenAI/responses"} {
		status, body := send(t, proxy, path, clientKeys())
		if status != http.StatusNotFound || !strings.Contains(body, "has no upstream named") {
			t.Errorf("%s: status = %d (%s); want 404, no upstream of that name", path, status, body)
		}
	}
	if n := len(claude.requests()) + len(openAI.requests()); n != 0 {
		t.Errorf("%d requests were forwarded; want none", n)
	}
}

// #58: a named upstream without its key yet answers 503, as the default
// one does, and takes the key up when the file arrives, with no restart.
func TestNamedUpstreamWaitsForItsKey(t *testing.T) {
	dir := t.TempDir()
	writeKey(t, dir, OAuthTokenKey, rand.Text(), then)
	claude, openAI := newUpstream(t), newUpstream(t)
	proxy := newNamedProxy(t, claude.URL, dir, "openai="+openAI.URL+",key="+openAIKeyFile)

	status, body := send(t, proxy, "/upstreams/openai/responses", clientKeys())
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "no credential for upstream openai") {
		t.Fatalf("status = %d (%s); want 503, no credential for the upstream", status, body)
	}
	if n := len(claude.requests()) + len(openAI.requests()); n != 0 {
		t.Fatalf("%d requests were forwarded without the upstream's key; want none", n)
	}
	key := rand.Text()
	writeKey(t, dir, openAIKeyFile, key, then)
	if status, body := send(t, proxy, "/upstreams/openai/responses", clientKeys()); status != http.StatusOK {
		t.Fatalf("status = %d (%s) once the key is there; want 200", status, body)
	}
	if creds := credentials(openAI.last(t)); !slices.Equal(creds, []string{"Authorization: Bearer " + key}) {
		t.Errorf("the upstream got the credentials %q; want its key alone", creds)
	}
}

func TestParseUpstream(t *testing.T) {
	for _, tt := range []struct {
		spec               string
		name, url, auth, k string
	}{
		{"openai=https://api.openai.com/v1,key=OPENAI_API_KEY", "openai", "https://api.openai.com/v1", AuthBearer, "OPENAI_API_KEY"},
		{"openai=https://api.openai.com/v1,auth=bearer,key=OPENAI_API_KEY", "openai", "https://api.openai.com/v1", AuthBearer, "OPENAI_API_KEY"},
		{"gemini=https://generativelanguage.googleapis.com,auth=header:X-Goog-Api-Key,key=GEMINI_API_KEY", "gemini",
			"https://generativelanguage.googleapis.com", "header:X-Goog-Api-Key", "GEMINI_API_KEY"},
		{"local=http://ollama.models.svc:11434/v1", "local", "http://ollama.models.svc:11434/v1", AuthNone, ""},
		{"local-2=http://vllm:8000/v1,auth=none", "local-2", "http://vllm:8000/v1", AuthNone, ""},
	} {
		up, err := ParseUpstream(tt.spec)
		if err != nil || up.Name != tt.name || up.URL.String() != tt.url || up.Auth != tt.auth || up.Key != tt.k {
			t.Errorf("ParseUpstream(%q) = %+v, %v; want %s to %s, auth %s, key %q", tt.spec, up, err,
				tt.name, tt.url, tt.auth, tt.k)
		}
	}
	for spec, want := range map[string]string{
		"https://api.openai.com/v1":                        "want <name>=<url>",
		"OpenAI=https://api.openai.com/v1":                 "want <name>=<url>",
		"openai=api.openai.com/v1":                         "not an http or https URL",
		"openai=ftp://api.openai.com":                      "not an http or https URL",
		"openai=https://user:pw@api.openai.com/v1":         "takes none",
		"openai=https://api.openai.com/v1?key=abc":         "takes none",
		"openai=https://api.openai.com/v1,token=abc":       "neither an auth=<how> nor a key=<file>",
		"openai=https://api.openai.com/v1,auth=basic":      "auth=basic: want",
		"openai=https://api.openai.com/v1,auth=bearer":     "needs key=<key file>",
		"openai=https://api.openai.com/v1,key=../etc/x":    "needs key=<key file>",
		"openai=https://api.openai.com/v1,auth=header:a b": "is not a header's name",
		"local=http://ollama:11434/v1,auth=none,key=K":     "takes no key",
	} {
		if _, err := ParseUpstream(spec); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("ParseUpstream(%q) error = %v; want one with %q", spec, err, want)
		}
	}
	a, _ := ParseUpstream("openai=https://a.example/v1")
	if _, err := NewWithUpstreams(mustURL(t, "https://api.anthropic.com"), []Upstream{a, a}, t.TempDir(), nil); err == nil ||
		!strings.Contains(err.Error(), "two upstreams are named openai") {
		t.Errorf("two upstreams of one name: error = %v; want it refused", err)
	}
}
