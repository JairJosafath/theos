/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package controller

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

// heldWebhookServer is the suite's webhook server. It serves the embedded
// server's mux on a listener the suite bound to port 0 before envtest
// started, and holds it until the suite ends. envtest writes the port into
// every CRD's conversion config, and controller-runtime's server binds it
// only when the manager starts: on a busy device another process took the
// port in between, and the suite never started (#2002).
type heldWebhookServer struct {
	webhook.Server
	listener net.Listener
	certDir  string
	started  atomic.Bool
}

// Start serves the webhook mux on the held listener with envtest's serving
// certificate until ctx ends.
func (s *heldWebhookServer) Start(ctx context.Context) error {
	cert, err := tls.LoadX509KeyPair(filepath.Join(s.certDir, "tls.crt"), filepath.Join(s.certDir, "tls.key"))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.WebhookMux(), ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}}}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	s.started.Store(true)
	if err := srv.ServeTLS(s.listener, "", ""); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// StartedChecker is healthy once Start serves; the listener has been bound
// since before envtest started.
func (s *heldWebhookServer) StartedChecker() healthz.Checker {
	return func(*http.Request) error {
		if !s.started.Load() {
			return errors.New("webhook server has not been started yet")
		}
		return nil
	}
}

// The suite's webhook port stays bound from before envtest picks it until
// the server serves on it, so no other process can take it in between
// (#2002).
func TestHeldWebhookServerHoldsItsPort(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	opts := envtest.WebhookInstallOptions{LocalServingHost: "127.0.0.1", LocalServingPort: port}
	if err := opts.PrepWithoutInstalling(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = opts.Cleanup() }()
	if opts.LocalServingPort != port {
		t.Fatalf("envtest picked port %d, not the held %d", opts.LocalServingPort, port)
	}
	if other, err := net.Listen("tcp", addr); !errors.Is(err, syscall.EADDRINUSE) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatalf("another listener on the held port: want EADDRINUSE, got %v", err)
	}

	server := &heldWebhookServer{Server: webhook.NewServer(webhook.Options{}), listener: listener,
		certDir: opts.LocalServingCertDir}
	server.Register("/convert", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "served")
	}))
	if err := server.StartedChecker()(nil); err == nil {
		t.Fatal("the server reports started before Start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Start(ctx) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Start: %v", err)
		}
	}()

	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(opts.LocalServingCAData)
	httpClient := &http.Client{Timeout: 10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	var body []byte
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := httpClient.Get("https://" + addr + "/convert")
		if err == nil {
			body, err = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
		}
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the server does not serve on the held port: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if string(body) != "served" {
		t.Fatalf("GET /convert = %q, want %q", body, "served")
	}
	if err := server.StartedChecker()(nil); err != nil {
		t.Fatalf("StartedChecker after Start: %v", err)
	}
}
