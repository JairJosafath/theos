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
