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

package main

import (
	"context"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseUpstream(t *testing.T) {
	for _, tt := range []struct {
		raw string
		ok  bool
	}{
		{raw: "https://api.anthropic.com", ok: true},
		{raw: "http://claude-api.test:8080/base", ok: true},
		{raw: "api.anthropic.com"},
		{raw: "ftp://api.anthropic.com"},
		{raw: "https://"},
		{raw: "https://api.anthropic.com/%zz"},
	} {
		if _, err := parseUpstream(tt.raw); (err == nil) != tt.ok {
			t.Errorf("parseUpstream(%q) error = %v; want ok %v", tt.raw, err, tt.ok)
		}
	}
}

// TestServeDrainsBeforeShutdown checks that a stop calls drain first and
// keeps the listener open for the drain period, so a request made while the
// Service's endpoints still name the pod is served, not refused (#1135).
func TestServeDrainsBeforeShutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	var served atomic.Int32
	server := &http.Server{
		Addr: addr,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			served.Add(1)
			w.WriteHeader(http.StatusOK)
		}),
		ReadHeaderTimeout: time.Second,
	}
	ctx, cancel := context.WithCancel(context.Background())
	drained := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, func() {}, server, func() { close(drained) }, 2*time.Second)
	}()

	client := &http.Client{Timeout: time.Second}
	get := func() (int, error) {
		resp, err := client.Get("http://" + addr + "/readyz")
		if err != nil {
			return 0, err
		}
		_ = resp.Body.Close()
		return resp.StatusCode, nil
	}
	waitFor(t, func() bool { _, err := get(); return err == nil })

	stopped := time.Now()
	cancel()
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("serve didn't call drain within a second of the stop")
	}
	if status, err := get(); err != nil || status != http.StatusOK {
		t.Errorf("a request during the drain: %d, %v; want 200 and no error", status, err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve didn't return after the drain")
	}
	if took := time.Since(stopped); took < 2*time.Second {
		t.Errorf("serve returned %v after the stop; want at least the 2s drain", took)
	}
	if _, err := get(); err == nil {
		t.Error("a request after serve returned was served; want the listener closed")
	}
}

// waitFor polls cond up to five seconds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("the server didn't come up in five seconds")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
