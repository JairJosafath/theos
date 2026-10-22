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
	sse := http.Header{"Content-Type": {"text/event-stream"}}
	for _, tt := range []struct {
		name   string
		header http.Header
		// slow sends the request's body in parts, each after the upstream has
		// sent the first event: a client still uploading when the answer
		// starts. By default the server consumes and closes the unread body
		// before it writes the response, under the transport still copying it
		// to the upstream, and the upstream request fails mid-stream (#1349).
		slow bool
	}{
		// What the API streams; httputil flushes these whatever FlushInterval says.
		{name: "server-sent events", header: sse},
		// Only FlushInterval: -1 flushes a body of known length as it comes.
		{name: "a body of known length", header: http.Header{
			"Content-Type": {"application/octet-stream"}, "Content-Length": {strconv.Itoa(len(first + rest))}}},
		{name: "a request body still arriving", header: sse, slow: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			release := make(chan struct{})
			sent := make(chan struct{}) // closed once the upstream has sent first
			sentFirst := sync.OnceFunc(func() { close(sent) })
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Answer before the request's body is in, as the API may.
				_ = http.NewResponseController(w).EnableFullDuplex()
				maps.Copy(w.Header(), tt.header)
				_, _ = io.WriteString(w, first)
				_ = http.NewResponseController(w).Flush()
				sentFirst()
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
				const payload = `{"a":1}`
				var body io.Reader = strings.NewReader(payload)
				if tt.slow {
					pr, pw := io.Pipe()
					body = pr
					go func() {
						select {
						case <-sent:
						case <-t.Context().Done():
							_ = pw.CloseWithError(t.Context().Err())
							return
						}
						// Three parts, each long enough after the last for the
						// server's drain, when there is one, to queue on the body.
						for _, part := range []string{`{"a"`, `:1`, `}`} {
							time.Sleep(20 * time.Millisecond)
							if _, err := io.WriteString(pw, part); err != nil {
								return
							}
						}
						_ = pw.Close()
					}()
				}
				req, err := http.NewRequest(http.MethodPost, proxy.URL+"/v1/messages", body)
				if err != nil {
					got <- read{err: err}
					return
				}
				req.ContentLength = int64(len(payload)) // the pipe's length is not known to the client
				resp, err := proxy.Client().Do(req)
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
			// Ready without the credential: the Secret comes after the chart (#1627).
			if status, _ := send(t, proxy, "/readyz", nil); status != http.StatusOK {
				t.Errorf("/readyz without a credential = %d; want 200", status)
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

// TestReadyWithoutACredentialSaysSo checks that a proxy with no credential
// is ready and says so in its log, and logs the credential's arrival on the
// next readiness probe, before any request is proxied (#1627).
func TestReadyWithoutACredentialSaysSo(t *testing.T) {
	logs := &syncBuffer{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	dir := t.TempDir()
	proxy := newProxy(t, newUpstream(t).URL, dir, log)

	if status, _ := send(t, proxy, "/readyz", nil); status != http.StatusOK {
		t.Fatalf("/readyz without a credential = %d; want 200", status)
	}
	if out := logs.String(); !strings.Contains(out, "No credential") {
		t.Errorf("the log doesn't say the proxy has no credential:\n%s", out)
	}
	if out := logs.String(); strings.Contains(out, "Using the credential") {
		t.Errorf("the log says the proxy uses a credential before it has one:\n%s", out)
	}

	token := rand.Text()
	writeKey(t, dir, OAuthTokenKey, token, then)
	if status, _ := send(t, proxy, "/readyz", nil); status != http.StatusOK {
		t.Fatalf("/readyz with a credential = %d; want 200", status)
	}
	out := logs.String()
	if !strings.Contains(out, "Using the credential") || !strings.Contains(out, OAuthTokenKey) {
		t.Errorf("the log doesn't say the proxy picked up %s on the probe:\n%s", OAuthTokenKey, out)
	}
	if strings.Contains(out, token) {
		t.Errorf("the log holds the token:\n%s", out)
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

// TestDrainFailsReadinessOnly checks that Drain turns /readyz to 503 while
// /healthz and the forwarded paths go on being served, so a stopping pod
// leaves the Service's endpoints before its listener closes (#1135).
func TestDrainFailsReadinessOnly(t *testing.T) {
	dir := t.TempDir()
	writeKey(t, dir, OAuthTokenKey, rand.Text(), then)
	up := newUpstream(t)
	p := New(mustURL(t, up.URL), dir, nil)
	proxy := httptest.NewServer(p)
	t.Cleanup(proxy.Close)

	if status, _ := send(t, proxy, "/readyz", nil); status != http.StatusOK {
		t.Fatalf("/readyz before Drain = %d; want 200", status)
	}
	p.Drain()
	if status, body := send(t, proxy, "/readyz", nil); status != http.StatusServiceUnavailable ||
		!strings.Contains(body, "stopping") {
		t.Errorf("/readyz after Drain = %d %q; want 503 saying it is stopping", status, body)
	}
	if status, _ := send(t, proxy, "/healthz", nil); status != http.StatusOK {
		t.Errorf("/healthz after Drain = %d; want 200", status)
	}
	if status, _ := send(t, proxy, "/v1/messages", clientHeader()); status != http.StatusOK {
		t.Errorf("/v1/messages after Drain = %d; want 200: open and new requests are still served", status)
	}
	if n := len(up.requests()); n != 1 {
		t.Errorf("the upstream was sent %d requests after Drain; want 1", n)
	}
}

// TestRetriesAFailedRead checks that a key file the proxy can't read gives 503
// and, once it can be read again with its modification time and size
// unchanged, 200 with no restart: the failed read isn't cached (#1286).
func TestRetriesAFailedRead(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a file of mode 000")
	}
	logs := &syncBuffer{}
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	dir := t.TempDir()
	writeKey(t, dir, OAuthTokenKey, rand.Text(), then)
	path := filepath.Join(dir, OAuthTokenKey)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	up := newUpstream(t)
	proxy := newProxy(t, up.URL, dir, log)

	for range 2 {
		if status, body := send(t, proxy, "/v1/messages", clientHeader()); status != http.StatusServiceUnavailable {
			t.Errorf("with an unreadable key file: %d %q; want 503", status, body)
		}
	}
	if n := strings.Count(logs.String(), "Failed to read a key file"); n != 1 {
		t.Errorf("the read failure was logged %d times; want once:\n%s", n, logs.String())
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || !info.ModTime().Equal(then) {
		t.Fatalf("the key file's mtime changed with chmod: %v %v", info.ModTime(), err)
	}
	if status, body := send(t, proxy, "/v1/messages", clientHeader()); status != http.StatusOK {
		t.Errorf("after the permission is fixed: %d %q; want 200", status, body)
	}
}

// TestRefusesAValueNoHeaderCanHold checks that a key file with a line break
// inside its value counts as no credential: 503 rather than the transport's
// 502 on every request, and a log that names the file and holds no part of
// the value (#1286).
func TestRefusesAValueNoHeaderCanHold(t *testing.T) {
	for _, key := range []string{OAuthTokenKey, APIKeyKey} {
		t.Run(key, func(t *testing.T) {
			logs := &syncBuffer{}
			log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			dir := t.TempDir()
			first, second := rand.Text(), rand.Text()
			writeKey(t, dir, key, first+"\n"+second+"\n", then)
			up := newUpstream(t)
			proxy := newProxy(t, up.URL, dir, log)

			if status, body := send(t, proxy, "/v1/messages", clientHeader()); status != http.StatusServiceUnavailable ||
				!strings.Contains(body, "no credential") {
				t.Errorf("with a line break inside the value: %d %q; want 503 saying it has no credential", status, body)
			}
			if n := len(up.requests()); n != 0 {
				t.Errorf("the upstream was sent %d requests; want none", n)
			}
			out := logs.String()
			if !strings.Contains(out, "can't be sent in a header") || !strings.Contains(out, key) {
				t.Errorf("the log doesn't name %s as holding a value no header can hold:\n%s", key, out)
			}
			for _, part := range []string{first, second} {
				if strings.Contains(out, part) {
					t.Errorf("the log holds part of the value (%q):\n%s", part, out)
				}
			}

			writeKey(t, dir, key, first, then.Add(time.Minute))
			if status, body := send(t, proxy, "/v1/messages", clientHeader()); status != http.StatusOK {
				t.Errorf("after the value is fixed: %d %q; want 200", status, body)
			}
		})
	}
}
