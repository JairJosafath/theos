/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"golang.org/x/mod/module"
)

// fakeGoProxy is the Go proxy's .info endpoint over httptest: a version is a
// 404, as the proxy answers one it can't fetch, until found makes it a 200.
type fakeGoProxy struct {
	server *httptest.Server
	mu     sync.Mutex
	// found and asked are by path@version.
	found map[string]bool
	asked map[string]int
}

func newFakeGoProxy() *fakeGoProxy {
	p := &fakeGoProxy{found: map[string]bool{}, asked: map[string]int{}}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	return p
}

func (p *fakeGoProxy) serve(w http.ResponseWriter, r *http.Request) {
	escPath, rest, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/@v/")
	escVersion, info := strings.CutSuffix(rest, ".info")
	path, err := module.UnescapePath(escPath)
	version, verr := module.UnescapeVersion(escVersion)
	if !ok || !info || err != nil || verr != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	key := path + "@" + version
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked[key]++
	if !p.found[key] {
		http.Error(w, "not found: "+key+": invalid version: unknown revision "+version, http.StatusNotFound)
		return
	}
	_, _ = w.Write([]byte(`{"Version":"` + version + `","Time":"2026-10-13T00:00:00Z"}`))
}

// setFound makes the proxy find path at version, or not.
func (p *fakeGoProxy) setFound(path, version string, found bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.found[path+"@"+version] = found
}

// asks is how often the proxy was asked for path at v0.1.0, the specs' release.
func (p *fakeGoProxy) asks(path string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.asked[path+"@v0.1.0"]
}
