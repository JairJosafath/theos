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

// Package modelproxy is the reverse proxy that keeps the factory's model
// keys out of agent sandboxes. It forwards a request to its upstream, drops
// every credential the client sent and adds the factory's, which it reads
// from a mounted Secret directory. A path under /upstreams/<name>/ goes to
// the upstream of that name, with that upstream's key; every other path
// goes to the default one, Claude's. It reads a key file again whenever the
// file changes, so a rotated Secret reaches the next request without a
// restart. It never logs a header.
package modelproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/net/http/httpguts"
)

// The key files the proxy reads in its credentials directory. When both hold
// a value, the OAuth token wins: it is the factory's default key.
const (
	OAuthTokenKey = "CLAUDE_CODE_OAUTH_TOKEN" //nolint:gosec // a key file's name, not a credential
	APIKeyKey     = "ANTHROPIC_API_KEY"       //nolint:gosec // a key file's name, not a credential
)

// OAuthBeta is the anthropic-beta value the API wants with an OAuth token.
const OAuthBeta = "oauth-2025-04-20"

// NamedPrefix starts the path of a request for a named upstream:
// /upstreams/<name>/<the upstream's own path>. A prefix of its own, and not
// the path's first segment: /v1/... is Claude's API, and a mistyped name
// must never send one vendor's request to another with that vendor's key.
const NamedPrefix = "/upstreams/"

// How a named upstream takes its key.
const (
	// AuthBearer sends "Authorization: Bearer <key>".
	AuthBearer = "bearer"
	// AuthNone sends no key: a server that asks for none.
	AuthNone = "none"
	// AuthHeaderPrefix starts an Auth that names the header the key goes in
	// as it is: "header:X-Goog-Api-Key".
	AuthHeaderPrefix = "header:"
)

// Upstream is a named upstream: where the requests under
// /upstreams/<Name>/ go, and the key they get.
type Upstream struct {
	// Name is the path segment after NamedPrefix.
	Name string
	URL  *url.URL
	// Auth is AuthBearer, AuthNone, or AuthHeaderPrefix and a header's name.
	Auth string
	// Key is the key's file in the credentials directory; empty with AuthNone.
	Key string
}

var (
	upstreamName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`)
	keyFileName  = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,252}$`)
)

// ParseUpstream reads a named upstream as the flag gives it:
// "<name>=<url>[,auth=bearer|header:<Header>|none][,key=<key file>]". With a
// key and no auth it is a bearer key; with neither, the upstream takes none.
func ParseUpstream(spec string) (Upstream, error) {
	parts := strings.Split(spec, ",")
	name, raw, ok := strings.Cut(parts[0], "=")
	if !ok || !upstreamName.MatchString(name) {
		return Upstream{}, fmt.Errorf("%q: want <name>=<url>, the name of lower-case letters, digits and dashes", spec)
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return Upstream{}, fmt.Errorf("upstream %s: %q is not an http or https URL", name, raw)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return Upstream{}, fmt.Errorf("upstream %s: its URL carries a query, a fragment or a user, and takes none", name)
	}
	up := Upstream{Name: name, URL: u}
	for _, part := range parts[1:] {
		key, value, _ := strings.Cut(part, "=")
		switch key {
		case "auth":
			up.Auth = value
		case "key":
			up.Key = value
		default:
			return Upstream{}, fmt.Errorf("upstream %s: %q is neither an auth=<how> nor a key=<file>", name, part)
		}
	}
	if up.Auth == "" {
		up.Auth = AuthNone
		if up.Key != "" {
			up.Auth = AuthBearer
		}
	}
	header, named := strings.CutPrefix(up.Auth, AuthHeaderPrefix)
	switch {
	case named && !httpguts.ValidHeaderFieldName(header):
		return Upstream{}, fmt.Errorf("upstream %s: %q is not a header's name", name, header)
	case !named && up.Auth != AuthBearer && up.Auth != AuthNone:
		return Upstream{}, fmt.Errorf("upstream %s: auth=%s: want %s, %s<Header> or %s", name, up.Auth,
			AuthBearer, AuthHeaderPrefix, AuthNone)
	case up.Auth == AuthNone && up.Key != "":
		return Upstream{}, fmt.Errorf("upstream %s: auth=%s takes no key", name, AuthNone)
	case up.Auth != AuthNone && !keyFileName.MatchString(up.Key):
		return Upstream{}, fmt.Errorf("upstream %s: auth=%s needs key=<key file>, a file's name in the credentials directory",
			name, up.Auth)
	}
	return up, nil
}

// credentialHeaders are the headers a client's key can come in, whatever
// the vendor. The proxy drops them all on every request, so the key it adds
// is the only one that leaves.
var credentialHeaders = []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Api-Key"}

// Proxy is the model proxy's http.Handler. It answers /healthz (always 200)
// and /readyz (200 until Drain, else 503) itself, and forwards every other
// path to its upstream, or answers 503 while it has no credential. A path
// under NamedPrefix goes to the named upstream, or answers 404 when no
// upstream has that name. Readiness
// doesn't wait for the credential: the Secret arrives after the chart (the
// guide's step 2), and a pod that takes connections and answers 503 says
// more to a sandbox than one with no endpoints (#1627).
type Proxy struct {
	upstream *url.URL
	named    map[string]Upstream
	dir      string
	log      *slog.Logger
	proxy    *httputil.ReverseProxy
	draining atomic.Bool

	mu      sync.Mutex
	files   map[string]keyFile // by key name
	using   string             // the key name in use, "" for none, to log a change once
	checked bool               // whether using has been set yet
	keyed   map[string]bool    // by named upstream: whether its key was there, to log a change once
}

// credentialKey is the context key ServeHTTP hands the credential to rewrite
// under, as a keyValue.
type credentialKey struct{}

// keyValue is a credential: the key file it came from and what it holds,
// and for a named upstream, the upstream.
type keyValue struct {
	key, value string
	named      *Upstream
}

// keyFile is what a key file held when it was last read.
type keyFile struct {
	info   os.FileInfo
	value  string
	failed bool // whether the read failed, to read it again
}

// New returns a Proxy to upstream that reads its credential from the key
// files in credentialsDir. A nil log is slog.Default().
func New(upstream *url.URL, credentialsDir string, log *slog.Logger) *Proxy {
	p, _ := NewWithUpstreams(upstream, nil, credentialsDir, log)
	return p
}

// NewWithUpstreams returns a Proxy to upstream, and to each of named under
// NamedPrefix and its name. Two upstreams of one name are an error.
func NewWithUpstreams(upstream *url.URL, named []Upstream, credentialsDir string, log *slog.Logger) (*Proxy, error) {
	if log == nil {
		log = slog.Default()
	}
	p := &Proxy{upstream: upstream, named: map[string]Upstream{}, dir: credentialsDir, log: log,
		files: map[string]keyFile{}, keyed: map[string]bool{}}
	for _, up := range named {
		if _, twice := p.named[up.Name]; twice {
			return nil, fmt.Errorf("two upstreams are named %s", up.Name)
		}
		p.named[up.Name] = up
	}
	p.proxy = &httputil.ReverseProxy{
		Rewrite: p.rewrite,
		// The API streams server-sent events; every write goes out at once.
		FlushInterval: -1,
		ErrorHandler:  p.upstreamFailed,
		ErrorLog:      slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return p, nil
}

// ServeHTTP answers the probes and forwards the rest with the credential.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	if r.URL.Path == "/readyz" {
		if p.draining.Load() {
			http.Error(w, "The model proxy is stopping.", http.StatusServiceUnavailable)
			return
		}
		// Ready with or without a credential; the read is for the log, so it
		// says within a probe period that the token arrived, not only on the
		// first request a sandbox sends (#1627).
		p.credential()
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	var credential keyValue
	if rest, named := strings.CutPrefix(r.URL.Path, NamedPrefix); named {
		name, _, _ := strings.Cut(rest, "/")
		up, ok := p.named[name]
		if !ok {
			// Nothing is forwarded: not to the default upstream either, which
			// would get another vendor's request.
			http.Error(w, fmt.Sprintf("The model proxy has no upstream named %q.", name), http.StatusNotFound)
			return
		}
		credential = keyValue{named: &up}
		if up.Auth != AuthNone {
			if credential.key, credential.value = up.Key, p.namedKey(up); credential.value == "" {
				http.Error(w, fmt.Sprintf("The model proxy has no credential for upstream %s yet.", up.Name),
					http.StatusServiceUnavailable)
				return
			}
		}
	} else {
		name, value := p.credential()
		if name == "" {
			http.Error(w, "The model proxy has no credential yet.", http.StatusServiceUnavailable)
			return
		}
		credential = keyValue{key: name, value: value}
	}
	ctx := context.WithValue(r.Context(), credentialKey{}, credential)
	// The upstream may answer while the request's body is still on its way
	// to it (the transport copies it in another goroutine). Without full
	// duplex the server consumes and closes the unread body as the answer's
	// headers go out, the transport's copy then fails on the closed body,
	// and the upstream request dies mid-stream (#1349). Only HTTP/1 needs
	// it; HTTP/2 streams both ways and reports it unsupported.
	_ = http.NewResponseController(w).EnableFullDuplex()
	p.proxy.ServeHTTP(w, r.WithContext(ctx))
}

// Drain makes /readyz answer 503 from now on while every other path goes on
// being served, so a stopping pod leaves the Service's endpoints before its
// listener closes and a sandbox's new connection is never refused. Requests
// already open are untouched.
func (p *Proxy) Drain() { p.draining.Store(true) }

// rewrite points the request at the upstream, its path and query joined onto
// the upstream's and its Host the upstream's, and swaps the client's
// credential for the one ServeHTTP read. ReverseProxy calls it after it has
// removed the hop-by-hop headers, so a client's Connection header can't name
// the credential away.
//
// A named upstream's request loses NamedPrefix and the name from its path,
// and gets that upstream's key the way the upstream takes it, or none.
func (p *Proxy) rewrite(pr *httputil.ProxyRequest) {
	c, _ := pr.In.Context().Value(credentialKey{}).(keyValue)
	h := pr.Out.Header
	for _, name := range credentialHeaders {
		h.Del(name)
	}
	if up := c.named; up != nil {
		// The name holds nothing a URL escapes, so the prefix reads the same
		// in the escaped path.
		prefix := NamedPrefix + up.Name
		pr.Out.URL.Path = strings.TrimPrefix(pr.Out.URL.Path, prefix)
		pr.Out.URL.RawPath = strings.TrimPrefix(pr.Out.URL.RawPath, prefix)
		pr.SetURL(up.URL)
		if header, named := strings.CutPrefix(up.Auth, AuthHeaderPrefix); named {
			h.Set(header, c.value)
		} else if up.Auth == AuthBearer {
			h.Set("Authorization", "Bearer "+c.value)
		}
		return
	}
	pr.SetURL(p.upstream)
	if c.key == OAuthTokenKey {
		h.Set("Authorization", "Bearer "+c.value)
		h.Set("Anthropic-Beta", withOAuthBeta(h.Values("Anthropic-Beta")))
	} else {
		h.Set("X-Api-Key", c.value)
	}
}

// upstreamFailed answers 502 when the upstream can't be reached. It logs the
// request's method and path, never its headers. A client that went away
// (Claude Code aborting a turn) is nobody's failure, and gets no answer.
func (p *Proxy) upstreamFailed(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, context.Canceled) {
		return
	}
	p.log.Error("Upstream request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	w.WriteHeader(http.StatusBadGateway)
}

// credential returns the key name and value of the credential to send, the
// OAuth token before the API key; an empty name when neither file holds one.
func (p *Proxy) credential() (name, value string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	name, value = OAuthTokenKey, p.read(OAuthTokenKey)
	if value == "" {
		name, value = APIKeyKey, p.read(APIKeyKey)
	}
	if value == "" {
		name = ""
	}
	if name != p.using || !p.checked {
		p.using, p.checked = name, true
		if name == "" {
			p.log.Warn("No credential in the credentials directory", "dir", p.dir)
		} else {
			p.log.Info("Using the credential", "key", name, "dir", p.dir)
		}
	}
	return name, value
}

// namedKey returns the key of a named upstream, "" while its file holds
// none. It logs once when the key arrives and once when it goes, naming the
// upstream and the file, never what the file holds.
func (p *Proxy) namedKey(up Upstream) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	value := p.read(up.Key)
	if had, checked := p.keyed[up.Name]; !checked || had != (value != "") {
		p.keyed[up.Name] = value != ""
		if value == "" {
			p.log.Warn("No credential for the upstream in the credentials directory", "upstream", up.Name,
				"key", up.Key, "dir", p.dir)
		} else {
			p.log.Info("Using the credential for the upstream", "upstream", up.Name, "key", up.Key, "dir", p.dir)
		}
	}
	return value
}

// read returns the trimmed contents of key file name, "" when it is missing,
// can't be read, holds only white space or holds what can't be sent as a
// header value (a newline inside it: the transport would refuse every request,
// #1286). It reads the file again only when the file it stats to (a kubelet's
// Secret volume links KEY to ..data/KEY and swaps ..data) is another one, or
// its modification time or size changed, or its last read failed: a fixed
// permission or a freed file descriptor changes none of those (#1286). Each
// failure is logged once per change of the file, naming the file and never
// what it holds. p.mu is held.
func (p *Proxy) read(name string) string {
	path := filepath.Join(p.dir, name)
	info, err := os.Stat(path)
	if err != nil {
		delete(p.files, name)
		return ""
	}
	f, ok := p.files[name]
	unchanged := ok && os.SameFile(f.info, info) &&
		f.info.ModTime().Equal(info.ModTime()) && f.info.Size() == info.Size()
	if unchanged && !f.failed {
		return f.value
	}
	data, err := os.ReadFile(path) //nolint:gosec // a key file of the proxy's own directory
	if err != nil {
		if !unchanged {
			p.log.Error("Failed to read a key file", "key", name, "dir", p.dir, "error", err)
		}
		p.files[name] = keyFile{info: info, failed: true}
		return ""
	}
	value := strings.TrimSpace(string(data))
	if !httpguts.ValidHeaderFieldValue(value) {
		p.log.Error("A key file holds what can't be sent in a header, such as a line break inside it",
			"key", name, "dir", p.dir)
		value = ""
	}
	p.files[name] = keyFile{info: info, value: value}
	return value
}

// withOAuthBeta returns the anthropic-beta values the client sent, in one
// comma-separated header, with OAuthBeta added once.
func withOAuthBeta(values []string) string {
	var merged []string
	for _, v := range values {
		for part := range strings.SplitSeq(v, ",") {
			if part = strings.TrimSpace(part); part != "" && !slices.Contains(merged, part) {
				merged = append(merged, part)
			}
		}
	}
	if !slices.Contains(merged, OAuthBeta) {
		merged = append(merged, OAuthBeta)
	}
	return strings.Join(merged, ",")
}
