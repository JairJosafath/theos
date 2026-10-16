/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package modelproxy

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// placeholder and clientKey are the credentials a sandbox's client sends; the
// proxy must drop both.
const (
	placeholder = "c4-model-proxy"
	clientKey   = "client-key"
)

// then is the modification time of the first key file a test writes.
var then = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// upstream is a fake Claude API that records the requests it was sent and
// answers each one 200.
type upstream struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func newUpstream(t *testing.T) *upstream {
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.reqs = append(u.reqs, r.Clone(r.Context()))
		u.mu.Unlock()
		_, _ = io.WriteString(w, "from upstream")
	}))
	t.Cleanup(u.Close)
	return u
}

// requests returns the requests the upstream was sent so far.
func (u *upstream) requests() []*http.Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.reqs)
}

// last returns the newest request the upstream was sent.
func (u *upstream) last(t *testing.T) *http.Request {
	t.Helper()
	reqs := u.requests()
	if len(reqs) == 0 {
		t.Fatal("the upstream was sent no request")
	}
	return reqs[len(reqs)-1]
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// newProxy serves a Proxy to upstreamURL that reads dir.
func newProxy(t *testing.T, upstreamURL, dir string, log *slog.Logger) *httptest.Server {
	t.Helper()
	proxy := httptest.NewServer(New(mustURL(t, upstreamURL), dir, log))
	t.Cleanup(proxy.Close)
	return proxy
}

// writeKey writes value to key file name in dir, modified at mtime.
func writeKey(t *testing.T, dir, name, value string, mtime time.Time) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// clientHeader is what Claude Code in a sandbox sends: the placeholder, and
// for the test an API key of its own as well.
func clientHeader() http.Header {
	return http.Header{"Authorization": {"Bearer " + placeholder}, "X-Api-Key": {clientKey}}
}

// send posts to path on the proxy with header and returns the status and body.
func send(t *testing.T, proxy *httptest.Server, path string, header http.Header) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, proxy.URL+path, strings.NewReader(`{"model":"claude"}`))
	if err != nil {
		t.Fatal(err)
	}
	maps.Copy(req.Header, header)
	resp, err := proxy.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func TestReplacesTheClientCredential(t *testing.T) {
	token := rand.Text()
	for _, tt := range []struct {
		key                string
		wantAuth, wantKeys []string
	}{
		{key: OAuthTokenKey, wantAuth: []string{"Bearer " + token}},
		{key: APIKeyKey, wantKeys: []string{token}},
	} {
		t.Run(tt.key, func(t *testing.T) {
			dir := t.TempDir()
			writeKey(t, dir, tt.key, token+"\n", then)
			up := newUpstream(t)
			proxy := newProxy(t, up.URL, dir, nil)

			if status, body := send(t, proxy, "/v1/messages", clientHeader()); status != http.StatusOK {
				t.Fatalf("status = %d (%s); want 200", status, body)
			}
			got := up.last(t)
			if auth := got.Header.Values("Authorization"); !slices.Equal(auth, tt.wantAuth) {
				t.Errorf("upstream got Authorization %q; want %q", auth, tt.wantAuth)
			}
			if keys := got.Header.Values("X-Api-Key"); !slices.Equal(keys, tt.wantKeys) {
				t.Errorf("upstream got x-api-key %q; want %q", keys, tt.wantKeys)
			}
		})
	}
}

func TestMergesTheOAuthBeta(t *testing.T) {
	for _, tt := range []struct {
		name, key  string
		sent, want []string
	}{
		{name: "none sent", key: OAuthTokenKey, want: []string{OAuthBeta}},
		{name: "the client's values kept", key: OAuthTokenKey, sent: []string{"a-1,b-2", "c-3"},
			want: []string{"a-1,b-2,c-3," + OAuthBeta}},
		{name: "the client sent it already", key: OAuthTokenKey, sent: []string{"a-1, " + OAuthBeta},
			want: []string{"a-1," + OAuthBeta}},
		{name: "an API key leaves it as sent", key: APIKeyKey, sent: []string{"a-1,b-2", "c-3"},
			want: []string{"a-1,b-2", "c-3"}},
		{name: "an API key adds none", key: APIKeyKey},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeKey(t, dir, tt.key, rand.Text(), then)
			up := newUpstream(t)
			proxy := newProxy(t, up.URL, dir, nil)

			header := clientHeader()
			if tt.sent != nil {
				header["Anthropic-Beta"] = tt.sent
			}
			if status, body := send(t, proxy, "/v1/messages", header); status != http.StatusOK {
				t.Fatalf("status = %d (%s); want 200", status, body)
			}
			if got := up.last(t).Header.Values("Anthropic-Beta"); !slices.Equal(got, tt.want) {
				t.Errorf("upstream got anthropic-beta %q; want %q", got, tt.want)
			}
		})
	}
}

func TestOAuthTokenWinsOverAPIKey(t *testing.T) {
	token, key := rand.Text(), rand.Text()
	dir := t.TempDir()
	writeKey(t, dir, OAuthTokenKey, token, then)
	writeKey(t, dir, APIKeyKey, key, then)
	up := newUpstream(t)
	proxy := newProxy(t, up.URL, dir, nil)

	send(t, proxy, "/v1/messages", clientHeader())
	got := up.last(t)
	if auth := got.Header.Get("Authorization"); auth != "Bearer "+token {
		t.Errorf("upstream got Authorization %q; want the OAuth token", auth)
	}
	if keys := got.Header.Values("X-Api-Key"); len(keys) != 0 {
		t.Errorf("upstream got x-api-key %q; want none", keys)
	}
}

// kubeletWrite writes token the way a kubelet's Secret volume does: the key
// file links to ..data/KEY, and ..data to a directory named for the version.
// Called again, it swaps ..data to the new directory atomically.
func kubeletWrite(t *testing.T, dir, version, token string, mtime time.Time) {
	t.Helper()
	data := "..2026_" + version
	if err := os.Mkdir(filepath.Join(dir, data), 0o755); err != nil {
		t.Fatal(err)
	}
	writeKey(t, filepath.Join(dir, data), OAuthTokenKey, token, mtime)
	if err := os.Symlink(data, filepath.Join(dir, "..data_tmp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, OAuthTokenKey)
	if _, err := os.Lstat(link); errors.Is(err, fs.ErrNotExist) {
		if err := os.Symlink(filepath.Join("..data", OAuthTokenKey), link); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPicksUpARotatedToken(t *testing.T) {
	later := then.Add(time.Minute)
	for _, tt := range []struct {
		name          string
		write, rotate func(t *testing.T, dir, token string)
	}{
		{name: "rewritten in place",
			write:  func(t *testing.T, dir, token string) { writeKey(t, dir, OAuthTokenKey, token, then) },
			rotate: func(t *testing.T, dir, token string) { writeKey(t, dir, OAuthTokenKey, token, later) }},
		{name: "a kubelet's Secret volume",
			write:  func(t *testing.T, dir, token string) { kubeletWrite(t, dir, "v1", token, then) },
			rotate: func(t *testing.T, dir, token string) { kubeletWrite(t, dir, "v2", token, later) }},
		// The new file has the old one's time and size; it is another file.
		{name: "a kubelet's Secret volume, at the same time",
			write:  func(t *testing.T, dir, token string) { kubeletWrite(t, dir, "v1", token, then) },
			rotate: func(t *testing.T, dir, token string) { kubeletWrite(t, dir, "v2", token, then) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			old, rotated := rand.Text(), rand.Text()
			dir := t.TempDir()
			tt.write(t, dir, old)
			up := newUpstream(t)
			proxy := newProxy(t, up.URL, dir, nil)

			send(t, proxy, "/v1/messages", clientHeader())
			if auth := up.last(t).Header.Get("Authorization"); auth != "Bearer "+old {
				t.Fatalf("upstream got Authorization %q before the rotation; want the old token", auth)
			}
			tt.rotate(t, dir, rotated)
			send(t, proxy, "/v1/messages", clientHeader())
			if auth := up.last(t).Header.Get("Authorization"); auth != "Bearer "+rotated {
				t.Errorf("upstream got Authorization %q after the rotation; want the rotated token", auth)
			}
		})
	}
}

func TestStreamsWithoutBuffering(t *testing.T) {
	first := "event: message_start\ndata: {}\n\n"
	rest := "event: message_stop\ndata: {}\n\n"
	for _, tt := range []struct {
		name   string
		header http.Header
	}{
		// What the API streams; httputil flushes these whatever FlushInterval says.
		{name: "server-sent events", header: http.Header{"Content-Type": {"text/event-stream"}}},
		// Only FlushInterval: -1 flushes a body of known length as it comes.
		{name: "a body of known length", header: http.Header{
			"Content-Type": {"application/octet-stream"}, "Content-Length": {strconv.Itoa(len(first + rest))}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				maps.Copy(w.Header(), tt.header)
				_, _ = io.WriteString(w, first)
				_ = http.NewResponseController(w).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
				_, _ = io.WriteString(w, rest)
			}))
			t.Cleanup(up.Close)
			dir := t.TempDir()
			writeKey(t, dir, OAuthTokenKey, rand.Text(), then)
			proxy := newProxy(t, up.URL, dir, nil)
			// Cleanups run last first: the upstream finishes before the servers close.
			finish := sync.OnceFunc(func() { close(release) })
			t.Cleanup(finish)

			type read struct {
				resp  *http.Response
				event string
				err   error
			}
			got := make(chan read, 1)
			go func() {
				resp, err := proxy.Client().Post(proxy.URL+"/v1/messages", "application/json", strings.NewReader("{}"))
				if err != nil {
					got <- read{err: err}
					return
				}
				buf := make([]byte, len(first))
				_, err = io.ReadFull(resp.Body, buf)
				got <- read{resp: resp, event: string(buf), err: err}
			}()
			var r read
			select {
			case r = <-got:
			case <-time.After(5 * time.Second):
				t.Fatal("the first event did not reach the client before the upstream finished")
			}
			if r.err != nil {
				t.Fatal(r.err)
			}
			defer func() { _ = r.resp.Body.Close() }()
			if r.event != first {
				t.Errorf("first event = %q; want %q", r.event, first)
			}
			finish()
			if tail, err := io.ReadAll(r.resp.Body); err != nil || string(tail) != rest {
				t.Errorf("rest of the stream = %q, %v; want %q", tail, err, rest)
			}
		})
	}
}

func TestAnswers503WithoutACredential(t *testing.T) {
	for _, tt := range []struct {
		name  string
		write func(t *testing.T, dir string)
	}{
		{name: "no key file", write: func(*testing.T, string) {}},
		{name: "white space only", write: func(t *testing.T, dir string) {
			writeKey(t, dir, OAuthTokenKey, " \n\t\n", then)
			writeKey(t, dir, APIKeyKey, "", then)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			tt.write(t, dir)
			up := newUpstream(t)
			proxy := newProxy(t, up.URL, dir, nil)

			if status, body := send(t, proxy, "/v1/messages", clientHeader()); status != http.StatusServiceUnavailable ||
				!strings.Contains(body, "no credential") {
				t.Errorf("without a credential: %d %q; want 503 saying it has no credential", status, body)
			}
			if n := len(up.requests()); n != 0 {
				t.Errorf("the upstream was sent %d requests without a credential; want none", n)
			}
			if status, _ := send(t, proxy, "/readyz", nil); status != http.StatusServiceUnavailable {
				t.Errorf("/readyz without a credential = %d; want 503", status)
			}
			if status, _ := send(t, proxy, "/healthz", nil); status != http.StatusOK {
				t.Errorf("/healthz without a credential = %d; want 200", status)
			}

			writeKey(t, dir, OAuthTokenKey, rand.Text(), then.Add(time.Minute))
			if status, _ := send(t, proxy, "/readyz", nil); status != http.StatusOK {
				t.Errorf("/readyz with a credential = %d; want 200", status)
			}
			if status, body := send(t, proxy, "/v1/messages", clientHeader()); status != http.StatusOK {
				t.Errorf("with a credential: %d %q; want 200", status, body)
			}
			if n := len(up.requests()); n != 1 {
				t.Errorf("the upstream was sent %d requests; want 1", n)
			}
			if status, _ := send(t, proxy, "/healthz", nil); status != http.StatusOK {
				t.Errorf("/healthz with a credential = %d; want 200", status)
			}
		})
	}
}

func TestForwardsPathAndQuery(t *testing.T) {
	for _, base := range []string{"", "/anthropic"} {
		t.Run("upstream path "+base, func(t *testing.T) {
			dir := t.TempDir()
			writeKey(t, dir, OAuthTokenKey, rand.Text(), then)
			up := newUpstream(t)
			proxy := newProxy(t, up.URL+base, dir, nil)

			req, err := http.NewRequest(http.MethodPost, proxy.URL+"/v1/messages?beta=true", strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			req.Host = "model-proxy.example:8080"
			resp, err := proxy.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()

			got := up.last(t)
			if got.URL.Path != base+"/v1/messages" || got.URL.RawQuery != "beta=true" {
				t.Errorf("upstream got %s?%s; want %s/v1/messages?beta=true", got.URL.Path, got.URL.RawQuery, base)
			}
			if want := mustURL(t, up.URL).Host; got.Host != want {
				t.Errorf("upstream got Host %q; want its own, %q", got.Host, want)
			}
		})
	}
}

// syncBuffer is a bytes.Buffer the servers' goroutines may log to.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestLogsNoCredential(t *testing.T) {
	logs := &syncBuffer{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	token, key := rand.Text(), rand.Text()
	oauthDir, keyDir := t.TempDir(), t.TempDir()
	writeKey(t, oauthDir, OAuthTokenKey, token, then)
	writeKey(t, keyDir, APIKeyKey, key, then)
	up := newUpstream(t)
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()

	for _, tt := range []struct {
		name, upstream, dir string
		want                int
	}{
		{name: "OAuth token, upstream up", upstream: up.URL, dir: oauthDir, want: http.StatusOK},
		{name: "OAuth token, upstream down", upstream: down.URL, dir: oauthDir, want: http.StatusBadGateway},
		{name: "API key, upstream down", upstream: down.URL, dir: keyDir, want: http.StatusBadGateway},
		{name: "no credential", upstream: up.URL, dir: t.TempDir(), want: http.StatusServiceUnavailable},
	} {
		proxy := newProxy(t, tt.upstream, tt.dir, log)
		header := clientHeader()
		header.Set("Anthropic-Beta", "a-1")
		if status, body := send(t, proxy, "/v1/messages?beta=true", header); status != tt.want {
			t.Errorf("%s: status = %d (%s); want %d", tt.name, status, body, tt.want)
		}
	}

	out := logs.String()
	if !strings.Contains(out, "Upstream request failed") {
		t.Errorf("the logs say nothing of the upstream being down:\n%s", out)
	}
	for _, secret := range []string{token, key, placeholder, clientKey} {
		if strings.Contains(out, secret) {
			t.Errorf("the logs hold a credential the proxy saw (%q):\n%s", secret, out)
		}
	}
}

// A client's Connection header names the headers a hop drops; naming the
// credential's must not strip the one the proxy adds.
func TestConnectionHeaderKeepsTheCredential(t *testing.T) {
	token := rand.Text()
	for _, tt := range []struct {
		key          string
		header, want string
		wantBeta     []string
	}{
		{key: OAuthTokenKey, header: "Authorization", want: "Bearer " + token, wantBeta: []string{OAuthBeta}},
		{key: APIKeyKey, header: "X-Api-Key", want: token},
	} {
		t.Run(tt.key, func(t *testing.T) {
			dir := t.TempDir()
			writeKey(t, dir, tt.key, token, then)
			up := newUpstream(t)
			proxy := newProxy(t, up.URL, dir, nil)

			header := clientHeader()
			header.Set("Connection", "Authorization, X-Api-Key, Anthropic-Beta")
			header.Set("Anthropic-Beta", "client-1")
			if status, body := send(t, proxy, "/v1/messages", header); status != http.StatusOK {
				t.Fatalf("status = %d (%s); want 200", status, body)
			}
			got := up.last(t)
			if v := got.Header.Values(tt.header); !slices.Equal(v, []string{tt.want}) {
				t.Errorf("upstream got %s %q; want %q", tt.header, v, tt.want)
			}
			if beta := got.Header.Values("Anthropic-Beta"); !slices.Equal(beta, tt.wantBeta) {
				t.Errorf("upstream got anthropic-beta %q; want %q", beta, tt.wantBeta)
			}
		})
	}
}
