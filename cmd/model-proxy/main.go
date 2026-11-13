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

// Command model-proxy keeps the factory's model keys out of agent
// sandboxes. It forwards a request to its upstream, the Claude API by
// default, with the client's credential swapped for the one in a mounted
// Secret directory, and answers /healthz and /readyz itself. A path under
// /upstreams/<name>/ goes to the --named-upstream of that name, with that
// upstream's key.
//
//	model-proxy [--listen :8080] [--upstream https://api.anthropic.com] [--credentials-dir DIR] [--drain 5s]
//	    [--named-upstream openai=https://api.openai.com/v1,key=OPENAI_API_KEY]...
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/JairJosafath/theos/internal/buildinfo"
	"github.com/JairJosafath/theos/internal/modelproxy"
)

// shutdownGrace is how long a stop waits for open requests, the drain
// included, under the kubelet's default grace period of 30 seconds.
const shutdownGrace = 25 * time.Second

// namedUpstreams collects the --named-upstream flags.
type namedUpstreams []modelproxy.Upstream

func (n *namedUpstreams) String() string {
	names := make([]string, 0, len(*n))
	for _, up := range *n {
		names = append(names, up.Name)
	}
	return strings.Join(names, ",")
}

func (n *namedUpstreams) Set(spec string) error {
	up, err := modelproxy.ParseUpstream(spec)
	if err != nil {
		return err
	}
	*n = append(*n, up)
	return nil
}

func main() {
	listen := flag.String("listen", ":8080", "Address to serve on")
	upstream := flag.String("upstream", "https://api.anthropic.com",
		"The URL every request is forwarded to, but those under "+modelproxy.NamedPrefix)
	var named namedUpstreams
	flag.Var(&named, "named-upstream", "An upstream for the requests under "+modelproxy.NamedPrefix+"<name>/, as "+
		"<name>=<url>[,auth=bearer|header:<Header>|none][,key=<key file in --credentials-dir>]: a key without "+
		"auth is a bearer key, neither is an upstream that takes none. Repeatable.")
	dir := flag.String("credentials-dir", "/etc/model-proxy/credentials",
		"The mounted Secret directory holding "+modelproxy.OAuthTokenKey+" or "+modelproxy.APIKeyKey)
	drain := flag.Duration("drain", 5*time.Second,
		"How long a stop keeps serving with /readyz failing before the listener closes, "+
			"so the Service's endpoints drop the pod first")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	target, err := parseUpstream(*upstream)
	if err != nil {
		log.Error("Invalid --upstream", "error", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("Starting model proxy", "listen", *listen, "upstream", target.Redacted(), "namedUpstreams", named.String(),
		"credentialsDir", *dir, "drain", *drain, "version", buildinfo.Version)
	proxy, err := modelproxy.NewWithUpstreams(target, named, *dir, log)
	if err != nil {
		log.Error("Invalid --named-upstream", "error", err)
		os.Exit(2)
	}
	server := &http.Server{
		Addr:    *listen,
		Handler: proxy,
		// No WriteTimeout: a streamed response lasts as long as the model writes.
		ReadHeaderTimeout: 30 * time.Second,
	}
	if err := serve(ctx, stop, server, proxy.Drain, *drain); err != nil {
		log.Error("Model proxy stopped", "error", err)
		os.Exit(1)
	}
	log.Info("Model proxy stopped")
}

// serve runs server until ctx is done, then stops the signals ctx was
// waiting for, calls drain and keeps serving for drainFor, so a failing
// /readyz takes the pod out of the Service's endpoints before the listener
// closes (#1135), and shuts the server down, letting open requests finish for
// up to shutdownGrace in all.
func serve(
	ctx context.Context, stop context.CancelFunc, server *http.Server, drain func(), drainFor time.Duration,
) error {
	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	// A second signal stops the process at once.
	stop()
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	drain()
	select {
	case <-time.After(drainFor):
	case err := <-errs:
		return err
	}
	err := server.Shutdown(shutdown)
	if errors.Is(err, context.DeadlineExceeded) {
		// A stream outlasted the grace period: cut it, as the kubelet would.
		err = server.Close()
	}
	if err != nil {
		return err
	}
	if err := <-errs; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// parseUpstream parses the --upstream URL, which needs an http or https
// scheme and a host.
func parseUpstream(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%q: want an http or https URL", raw)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%q: no host", raw)
	}
	return u, nil
}
