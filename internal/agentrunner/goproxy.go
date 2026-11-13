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

package agentrunner

import (
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// FactoryModules is the prefix of the module path the Go templates give an
// app (theos.local/<project>/<container>). No public proxy knows it.
const FactoryModules = "theos.local"

// GoProxyAddr is where theos-agent serves GoProxy in the sandbox, and GoProxyURL
// the GOPROXY the operator sets for it: the go command asks it first and,
// for a 404 or 410, fetches directly.
const (
	GoProxyAddr = "127.0.0.1:3127"
	GoProxyURL  = "http://" + GoProxyAddr + ",direct"
)

// PublicGoProxy is the proxy for every module that isn't the factory's.
const PublicGoProxy = "https://proxy.golang.org"

// GoProxy is the Go module proxy theos-agent serves on loopback. It sends the
// factory's module paths to the owner's Go package registry and nowhere else,
// and everything else, the checksum database's proxy included, to the public
// proxy. The go command has no per-path proxy: with
// GOPROXY=<registry>,<public>, a module the registry lacks (a 404) is asked of
// the public proxy next, which would learn the project's and Container's names.
// Here a factory module the registry lacks is a 404, the go command goes on to
// direct, and theos.local resolves nowhere.
type GoProxy struct {
	// Registry is the owner's Go package registry; Public the public proxy.
	Registry, Public *url.URL
	// Username and Token are sent to the registry over https only, as the go
	// command sends a .netrc's; over http a public owner's packages need none.
	Username, Token string
	// Client makes the requests; nil is http.DefaultClient.
	Client *http.Client
}

// NewGoProxy returns the proxy for the job's registry, or nil when it has none.
func NewGoProxy(job Job) (*GoProxy, error) {
	if job.GoRegistry == "" {
		return nil, nil
	}
	registry, err := url.Parse(job.GoRegistry)
	if err != nil {
		return nil, err
	}
	public, err := url.Parse(PublicGoProxy)
	if err != nil {
		return nil, err
	}
	return &GoProxy{Registry: registry, Public: public, Username: job.Username, Token: job.Token}, nil
}

// IsFactoryModule reports whether an escaped module path, or a proxy request
// path under it, is the factory's.
func IsFactoryModule(p string) bool {
	p = strings.TrimPrefix(p, "/")
	return p == FactoryModules || strings.HasPrefix(p, FactoryModules+"/")
}

func (p *GoProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Routed and forwarded as cleaned, so a path can't step out of its prefix.
	clean := path.Clean("/" + r.URL.Path)
	upstream, private := p.Public, false
	if IsFactoryModule(clean) {
		upstream, private = p.Registry, true
	}
	target := *upstream
	target.Path = strings.TrimSuffix(upstream.Path, "/") + clean
	target.RawPath = ""
	target.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if private && target.Scheme == "https" && p.Token != "" {
		req.SetBasicAuth(p.Username, p.Token)
	}
	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req) //nolint:gosec // to a configured upstream, by a cleaned path
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	for _, h := range []string{"Content-Type", "Content-Length", "Cache-Control", "Last-Modified", "ETag"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
