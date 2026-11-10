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

package controller

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/google/go-containerregistry/pkg/registry"
)

// fakeRegistry is go-containerregistry's in-memory registry over httptest,
// counting the manifests it is given: GHCR or the owner's registry in tests.
// go-containerregistry speaks plain HTTP to 127.0.0.1.
type fakeRegistry struct {
	server *httptest.Server
	// host is the registry's host:port.
	host string
	puts atomic.Int64
}

func newFakeRegistry() *fakeRegistry {
	f := &fakeRegistry{}
	h := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/manifests/") {
			f.puts.Add(1)
		}
		h.ServeHTTP(w, r)
	}))
	f.host = strings.TrimPrefix(f.server.URL, "http://")
	return f
}
