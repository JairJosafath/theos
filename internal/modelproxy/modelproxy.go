/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

// Package modelproxy is the reverse proxy that keeps the factory's Claude
// token out of agent sandboxes. It forwards every request to one upstream,
// drops the credential the client sent and adds the factory's, which it reads
// from a mounted Secret directory. It reads the key file again whenever the
// file changes, so a rotated Secret reaches the next request without a
// restart. It never logs a header.
package modelproxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// The key files the proxy reads in its credentials directory. When both hold
// a value, the OAuth token wins: it is the factory's default key.
const (
	OAuthTokenKey = "CLAUDE_CODE_OAUTH_TOKEN"
	APIKeyKey     = "ANTHROPIC_API_KEY"
)

// OAuthBeta is the anthropic-beta value the API wants with an OAuth token.
const OAuthBeta = "oauth-2025-04-20"

// Proxy is the model proxy's http.Handler. It answers /healthz (always 200)
// and /readyz (200 once it has a credential, else 503) itself, and forwards
// every other path to its upstream, or answers 503 while it has no credential.
type Proxy struct {
	dir   string
	log   *slog.Logger
	proxy *httputil.ReverseProxy

	mu      sync.Mutex
	files   map[string]keyFile // by key name
	using   string             // the key name in use, "" for none, to log a change once
	checked bool               // whether using has been set yet
}

// keyFile is what a key file held when it was last read.
type keyFile struct {
	info  os.FileInfo
	value string
}

// New returns a Proxy to upstream that reads its credential from the key
// files in credentialsDir. A nil log is slog.Default().
func New(upstream *url.URL, credentialsDir string, log *slog.Logger) *Proxy {
	if log == nil {
		log = slog.Default()
	}
	p := &Proxy{dir: credentialsDir, log: log, files: map[string]keyFile{}}
	p.proxy = &httputil.ReverseProxy{
		// The path and query join onto the upstream's, and Host becomes the
		// upstream's. ServeHTTP has set the credential on the request already.
		Rewrite: func(pr *httputil.ProxyRequest) { pr.SetURL(upstream) },
		// The API streams server-sent events; every write goes out at once.
		FlushInterval: -1,
		ErrorHandler:  p.upstreamFailed,
		ErrorLog:      slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
	return p
}

// ServeHTTP answers the probes and forwards the rest with the credential.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	name, value := p.credential()
	switch {
	case name == "":
		http.Error(w, "The model proxy has no credential yet.", http.StatusServiceUnavailable)
		return
	case r.URL.Path == "/readyz":
		_, _ = io.WriteString(w, "ok\n")
		return
	}
	out := r.Clone(r.Context())
	out.Header.Del("Authorization")
	out.Header.Del("X-Api-Key")
	if name == OAuthTokenKey {
		out.Header.Set("Authorization", "Bearer "+value)
		out.Header.Set("Anthropic-Beta", withOAuthBeta(out.Header.Values("Anthropic-Beta")))
	} else {
		out.Header.Set("X-Api-Key", value)
	}
	p.proxy.ServeHTTP(w, out)
}

// upstreamFailed answers 502 when the upstream can't be reached. It logs the
// request's method and path, never its headers.
func (p *Proxy) upstreamFailed(w http.ResponseWriter, r *http.Request, err error) {
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

// read returns the trimmed contents of key file name, "" when it is missing
// or holds only white space. It reads the file again only when the file it
// stats to (a kubelet's Secret volume links KEY to ..data/KEY and swaps
// ..data) is another one, or its modification time or size changed. p.mu is
// held.
func (p *Proxy) read(name string) string {
	path := filepath.Join(p.dir, name)
	info, err := os.Stat(path)
	if err != nil {
		delete(p.files, name)
		return ""
	}
	if f, ok := p.files[name]; ok && os.SameFile(f.info, info) &&
		f.info.ModTime().Equal(info.ModTime()) && f.info.Size() == info.Size() {
		return f.value
	}
	data, err := os.ReadFile(path)
	if err != nil {
		// Logged once per change of the file: the failure is cached too.
		p.log.Error("Failed to read a key file", "error", err)
		data = nil
	}
	f := keyFile{info: info, value: strings.TrimSpace(string(data))}
	p.files[name] = f
	return f.value
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
