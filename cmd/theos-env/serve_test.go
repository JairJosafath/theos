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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/model"
)

// The one-run lock is let go before the exit frame goes out (#321): a run
// that starts after the first one ended, as its caller sees it, is let in
// even while the first handler is still on its way out. The server's
// afterExit seam holds the first handler open after the exit frame until the
// second run is done, so the window is as wide as the test needs.
func TestRunAfterAnEndedRunIsLetInWhileTheHandlerLingers321(t *testing.T) {
	repo := gitRepo(t)
	secondDone := make(chan struct{})
	var closeOnce sync.Once
	finish := func() { closeOnce.Do(func() { close(secondDone) }) }
	url, token, _ := environmentAfterExit(t, func() {
		select {
		case <-secondDone:
		case <-time.After(30 * time.Second):
		}
	})
	t.Cleanup(finish) // runs before the server closes, which waits for the handler

	first, _, stderr := runIn(t, repo, url, token, "mkdir -p node_modules && echo dep > node_modules/dep.js")
	if first != 0 {
		t.Fatalf("first run: exit %d: %s", first, stderr)
	}
	code, stdout, stderr := runIn(t, repo, url, token, "test -e node_modules/dep.js && echo leftover; echo second")
	finish()
	if code != 0 || strings.Contains(stdout, "leftover") || !strings.Contains(stdout, "second") {
		t.Errorf("second run, while the first handler lingers: exit %d, stdout %q, stderr %q; want it let in on a fresh tree",
			code, stdout, stderr)
	}
}

// signal closes its channel the first time it is written to.
type signal struct {
	once sync.Once
	ch   chan struct{}
}

func (s *signal) Write(p []byte) (int, error) {
	s.once.Do(func() { close(s.ch) })
	return len(p), nil
}

// One run at a time stays the rule: a run that starts while another is
// still running is refused with 409, and the run after both are over is let
// in (#321).
func TestRunWhileAnotherRunsIsRefusedAndTheLockIsFreeAfterwards321(t *testing.T) {
	repo := gitRepo(t)
	url, token, work := environment(t)
	t.Chdir(repo)

	// The first command says it started, then waits for the test to open and
	// close the fifo.
	fifo := filepath.Join(t.TempDir(), "release")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	started := &signal{ch: make(chan struct{})}
	first := caller{name: "browser", url: url, token: token, max: 1 << 20, stdout: started, stderr: &bytes.Buffer{}}
	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := first.run(context.Background(), []string{"sh", "-c", "echo started; cat " + fifo})
		done <- result{code, err}
	}()
	release := func() {
		f, err := os.OpenFile(fifo, os.O_RDWR, 0) // never blocks; closing it ends the reader's wait
		if err == nil {
			_ = f.Close()
		}
	}
	t.Cleanup(release)
	select {
	case <-started.ch:
	case <-time.After(30 * time.Second):
		t.Fatal("the first run never started")
	}

	second := caller{name: "browser", url: url, token: token, max: 1 << 20,
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if _, err := second.run(context.Background(), []string{"true"}); err == nil || !strings.Contains(err.Error(), "409") {
		t.Errorf("a run while another runs = %v, want it refused with 409", err)
	}
	// The refused request must not have emptied the tree the first run has.
	if _, err := os.Stat(filepath.Join(work, "a.txt")); err != nil {
		t.Errorf("after the refused run, the running one's tree lost a.txt: %v", err)
	}

	w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	select {
	case res := <-done:
		if res.err != nil || res.code != 0 {
			t.Errorf("the first run: exit %d, %v; want it to finish", res.code, res.err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the first run never finished")
	}

	third := caller{name: "browser", url: url, token: token, max: 1 << 20,
		stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
	if code, err := third.run(context.Background(), []string{"true"}); err != nil || code != 0 {
		t.Errorf("a run after both ended: exit %d, %v; want it let in", code, err)
	}
}

// A request the server refuses after it took the lock lets it go (#321): a
// tree that is no tar is answered 400, and the next run is let in.
func TestRunAfterARefusedTreeIsLetIn321(t *testing.T) {
	repo := gitRepo(t)
	url, token, _ := environment(t)
	req, _ := http.NewRequest(http.MethodPost, url+"/run", strings.NewReader("this is no tar"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(headerCommand, `["true"]`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a body that is no tree was answered %s, want 400", resp.Status)
	}
	if code, _, stderr := runIn(t, repo, url, token, "true"); code != 0 {
		t.Errorf("a run after a refused tree: exit %d: %s", code, stderr)
	}
}

// lockEnv is a server behind httptest that says when a handler has returned.
type lockEnv struct {
	srv         *server
	url, token  string
	work, repo  string
	handlerDone chan struct{}
}

// A run that ends any way but the exit frame lets the lock go (#321): once
// the handler has returned, the lock is free and the next run is let in.
// Each case causes one return path, then the same checks follow.
func TestRunLockIsFreeAfterEveryReturnPath321(t *testing.T) {
	cases := []struct {
		name      string
		max       int64
		afterExit func()
		cause     func(t *testing.T, e *lockEnv)
	}{
		{
			name: "413 tree over the cap",
			max:  1,
			cause: func(t *testing.T, e *lockEnv) {
				t.Helper()
				c := caller{name: "browser", url: e.url, token: e.token, max: 1 << 20,
					stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
				if _, err := c.run(context.Background(), []string{"true"}); err == nil || !strings.Contains(err.Error(), "413") {
					t.Fatalf("a tree over the cap = %v, want 413", err)
				}
			},
		},
		{
			// emptyDir cannot read a work folder that is gone.
			name: "500 work folder missing",
			cause: func(t *testing.T, e *lockEnv) {
				t.Helper()
				if err := os.RemoveAll(e.work); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.MkdirAll(e.work, 0o755) })
				c := caller{name: "browser", url: e.url, token: e.token, max: 1 << 20,
					stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
				if _, err := c.run(context.Background(), []string{"true"}); err == nil || !strings.Contains(err.Error(), "500") {
					t.Fatalf("a run with no work folder = %v, want 500", err)
				}
				if err := os.MkdirAll(e.work, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			// The folder to run in lies below a.txt, a file of the tree.
			name: "500 folder to run in is below a file",
			cause: func(t *testing.T, e *lockEnv) {
				t.Helper()
				var body bytes.Buffer
				gz := gzip.NewWriter(&body)
				tw := tar.NewWriter(gz)
				if err := tw.WriteHeader(&tar.Header{Name: "a.txt", Mode: 0o644, Size: 2, Typeflag: tar.TypeReg}); err != nil {
					t.Fatal(err)
				}
				_, _ = tw.Write([]byte("a\n"))
				if err := tw.Close(); err != nil {
					t.Fatal(err)
				}
				if err := gz.Close(); err != nil {
					t.Fatal(err)
				}
				req, _ := http.NewRequest(http.MethodPost, e.url+"/run", &body)
				req.Header.Set("Authorization", "Bearer "+e.token)
				req.Header.Set(headerCommand, `["true"]`)
				req.Header.Set(headerDir, "a.txt/below")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_ = resp.Body.Close()
				if resp.StatusCode != http.StatusInternalServerError {
					t.Fatalf("a folder below a file was answered %s, want 500", resp.Status)
				}
			},
		},
		{
			// A panic net/http recovers, raised by the afterExit seam: after
			// the lock was let go and the exit frame written.
			name:      "panic net/http recovers",
			afterExit: func() { panic("a test's panic after the exit frame") },
			cause: func(t *testing.T, e *lockEnv) {
				t.Helper()
				c := caller{name: "browser", url: e.url, token: e.token, max: 1 << 20,
					stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}}
				_, _ = c.run(context.Background(), []string{"true"}) // the connection is cut after the frame
			},
		},
		{
			name: "client goes away during exec",
			cause: func(t *testing.T, e *lockEnv) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				started := &signal{ch: make(chan struct{})}
				c := caller{name: "browser", url: e.url, token: e.token, max: 1 << 20,
					stdout: started, stderr: &bytes.Buffer{}}
				done := make(chan error, 1)
				go func() {
					_, err := c.run(ctx, []string{"sh", "-c", "echo started; exec sleep 30"})
					done <- err
				}()
				select {
				case <-started.ch:
				case <-time.After(30 * time.Second):
					t.Fatal("the run never started")
				}
				cancel()
				select {
				case <-done:
				case <-time.After(30 * time.Second):
					t.Fatal("the caller never returned after it was cancelled")
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := &lockEnv{repo: gitRepo(t), token: "tok", work: t.TempDir(), handlerDone: make(chan struct{}, 16)}
			e.srv = &server{token: e.token, work: e.work, max: model.TestEnvMaxTree, afterExit: tc.afterExit}
			if tc.max != 0 {
				e.srv.max = tc.max
			}
			ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer func() {
					select {
					case e.handlerDone <- struct{}{}:
					default:
					}
				}()
				e.srv.ServeHTTP(w, r)
			}))
			ts.Config.ErrorLog = log.New(io.Discard, "", 0) // the recovered panic's trace
			ts.Start()
			t.Cleanup(ts.Close)
			e.url = ts.URL
			t.Chdir(e.repo)

			tc.cause(t, e)
			select {
			case <-e.handlerDone:
			case <-time.After(30 * time.Second):
				t.Fatal("the handler never returned")
			}
			if !e.srv.busy.TryLock() {
				t.Fatal("the lock is still held after the handler returned")
			}
			e.srv.busy.Unlock()

			e.srv.max = model.TestEnvMaxTree // the handler has returned: nothing reads it now
			if code, _, stderr := runIn(t, e.repo, e.url, e.token, "true"); code != 0 {
				t.Errorf("a run after %s: exit %d: %s; want it let in", tc.name, code, stderr)
			}
		})
	}
}
