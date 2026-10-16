/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

// Command model-proxy keeps the factory's Claude token out of agent
// sandboxes. It forwards every request to one upstream, the Claude API by
// default, with the client's credential swapped for the one in a mounted
// Secret directory, and answers /healthz and /readyz itself.
//
//	model-proxy [--listen :8080] [--upstream https://api.anthropic.com] [--credentials-dir DIR]
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
	"syscall"
	"time"

	"github.com/JairJosafath/c4factory/internal/buildinfo"
	"github.com/JairJosafath/c4factory/internal/modelproxy"
)

// shutdownGrace is how long a stop waits for open requests, under the
// kubelet's default grace period of 30 seconds.
const shutdownGrace = 25 * time.Second

func main() {
	listen := flag.String("listen", ":8080", "Address to serve on")
	upstream := flag.String("upstream", "https://api.anthropic.com", "The one URL every request is forwarded to")
	dir := flag.String("credentials-dir", "/etc/model-proxy/credentials",
		"The mounted Secret directory holding "+modelproxy.OAuthTokenKey+" or "+modelproxy.APIKeyKey)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	target, err := parseUpstream(*upstream)
	if err != nil {
		log.Error("Invalid --upstream", "error", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("Starting model proxy", "listen", *listen, "upstream", target.Redacted(), "credentialsDir", *dir,
		"version", buildinfo.Version)
	server := &http.Server{
		Addr:    *listen,
		Handler: modelproxy.New(target, *dir, log),
		// No WriteTimeout: a streamed response lasts as long as the model writes.
		ReadHeaderTimeout: 30 * time.Second,
	}
	if err := serve(ctx, server); err != nil {
		log.Error("Model proxy stopped", "error", err)
		os.Exit(1)
	}
	log.Info("Model proxy stopped")
}

// serve runs server until ctx is done, then shuts it down, letting open
// requests finish for up to shutdownGrace.
func serve(ctx context.Context, server *http.Server) error {
	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()
	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
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
