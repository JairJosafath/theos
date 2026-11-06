/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

	"github.com/JairJosafath/theos/internal/model"
)

// envToken is the variable serve reads the lease's token from; the
// commands it runs never see it.
const envToken = "THEOS_ENV_TOKEN" //nolint:gosec // an environment variable's name

// serveMain serves runs until it is killed: the lease pod's process.
func serveMain(args []string, stderr io.Writer, getenv func(string) string) int {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", ":"+strconv.Itoa(model.TestEnvPort), "the address to listen on")
	work := flags.String("work", "/work", "the folder each run's tree goes in")
	useTLS := flags.Bool("tls", false, "serve HTTPS with the certificate the lease's token makes, which the run "+
		"pins: a remote environment, reached over the internet")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	token := getenv(envToken)
	if token == "" {
		_, _ = fmt.Fprintf(stderr, "theos-env: %s is unset; serve takes runs with the lease's token alone\n", envToken)
		return exitFailed
	}
	if err := os.MkdirAll(*work, 0o750); err != nil {
		_, _ = fmt.Fprintf(stderr, "theos-env: %v\n", err)
		return exitFailed
	}
	srv := &http.Server{Addr: *listen, Handler: &server{token: token, work: *work, max: model.TestEnvMaxTree},
		ReadHeaderTimeout: 30 * time.Second}
	serve, scheme := srv.ListenAndServe, "http"
	if *useTLS {
		config, err := serveTLS(token)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "theos-env: %v\n", err)
			return exitFailed
		}
		srv.TLSConfig = config
		serve, scheme = func() error { return srv.ListenAndServeTLS("", "") }, "https"
	}
	_, _ = fmt.Fprintf(stderr, "theos-env: serving runs over %s on %s in %s\n", scheme, *listen, *work)
	if err := serve(); err != nil {
		_, _ = fmt.Fprintf(stderr, "theos-env: %v\n", err)
	}
	return exitFailed
}

// serveTLS is the TLS a remote environment serves with: the certificate and
// key token makes (model.TestEnvCertificate), the same the operator gave
// the run to pin, so no key travels to the machine (T4, T5).
func serveTLS(token string) (*tls.Config, error) {
	certPEM, key, err := model.TestEnvCertificate(token)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, errors.New("the lease's certificate is no PEM")
	}
	return &tls.Config{MinVersion: tls.VersionTLS12,
		Certificates: []tls.Certificate{{Certificate: [][]byte{block.Bytes}, PrivateKey: key}}}, nil
}

// server takes runs: one at a time, each on a fresh copy of the caller's
// tree in work.
type server struct {
	token string
	work  string
	// max is how many bytes of files a tree, sent or brought back, holds at
	// most.
	max  int64
	busy sync.Mutex
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		_, _ = io.WriteString(w, "ok\n")
	case r.URL.Path == "/run" && r.Method == http.MethodPost:
		s.run(w, r)
	default:
		http.NotFound(w, r)
	}
}

// authorized reports whether r carries the lease's token.
func (s *server) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

func (s *server) run(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		http.Error(w, "the lease's token is wrong or missing", http.StatusUnauthorized)
		return
	}
	var argv []string
	if err := json.Unmarshal([]byte(r.Header.Get(headerCommand)), &argv); err != nil || len(argv) == 0 {
		http.Error(w, "no command to run", http.StatusBadRequest)
		return
	}
	dir := filepath.FromSlash(r.Header.Get(headerDir))
	if dir == "" {
		dir = "."
	}
	if !filepath.IsLocal(dir) {
		http.Error(w, "the folder to run in is outside the tree", http.StatusBadRequest)
		return
	}
	if !s.busy.TryLock() {
		http.Error(w, "a run is going on in this environment; one runs at a time", http.StatusConflict)
		return
	}
	defer s.busy.Unlock()

	// Each run gets the tree it sent, nothing a run before left.
	if err := emptyDir(s.work); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	root, err := os.OpenRoot(s.work)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer func() { _ = root.Close() }()
	sent, err := readTree(r.Body, root, s.max)
	switch {
	case errors.Is(err, errOverCap):
		http.Error(w, fmt.Sprintf("the tree is over the %d MiB a run sends", s.max>>20), http.StatusRequestEntityTooLarge)
		return
	case err != nil:
		http.Error(w, "reading the tree: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := root.MkdirAll(dir, 0o755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	flusher, _ := w.(http.Flusher)
	fw := &frameWriter{w: w, flush: func() {
		if flusher != nil {
			flusher.Flush()
		}
	}}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	code := s.exec(r, argv, filepath.Join(s.work, dir), fw)
	s.bringBack(root, sent, fw)
	_ = fw.frame(frameExit, []byte(strconv.Itoa(code)))
}

// exec runs argv in dir, its output in frames, and returns its exit code.
// It stops the command when the caller goes; what the command started in
// the background keeps running, as an emulator booted once should.
func (s *server) exec(r *http.Request, argv []string, dir string, fw *frameWriter) int {
	// The lease holder's command, run where it asked: what the lease is for.
	cmd := exec.CommandContext(r.Context(), argv[0], argv[1:]...) //nolint:gosec // see above
	cmd.Dir = dir
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, envToken+"=") })
	cmd.Stdout, cmd.Stderr = stream{fw, frameStdout}, stream{fw, frameStderr}
	// A background process that keeps the output open doesn't hold the run.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		return exit.ExitCode()
	case cmd.ProcessState != nil && cmd.ProcessState.Success():
		// Done, but WaitDelay closed the output a background process held.
		return 0
	}
	_ = fw.frame(frameMessage, []byte(fmt.Sprintf("could not run %s: %v", argv[0], err)))
	return 127
}

// bringBack sends the files the command changed or added, the tree's
// ignored ones left out: those it sent with other contents, and new ones.
func (s *server) bringBack(root *os.Root, sent []treeFile, fw *frameWriter) {
	before := make(map[string][sha256.Size]byte, len(sent))
	for _, f := range sent {
		before[f.name] = f.sum
	}
	changed, total, err := changedFiles(s.work, before)
	if err == nil && total > s.max {
		err = fmt.Errorf("the changed files are over the %d MiB a run brings back", s.max>>20)
	}
	if err != nil {
		_ = fw.frame(frameMessage, []byte("brought back no file: "+err.Error()))
		return
	}
	if len(changed) == 0 {
		return
	}
	if err := writeTree(stream{fw, frameFiles}, root, changed); err != nil {
		_ = fw.frame(frameMessage, []byte("bringing back the files: "+err.Error()))
	}
}

// changedFiles walks work for the files and links that differ from before
// or are new, by their bytes, skipping what the tree's .gitignore files
// ignore (node_modules, build output) and any .git folder, and sums their
// sizes.
func changedFiles(work string, before map[string][sha256.Size]byte) ([]string, int64, error) {
	patterns, err := gitignore.ReadPatterns(osfs.New(work), nil)
	if err != nil {
		return nil, 0, err
	}
	ignored := gitignore.NewMatcher(patterns)
	var changed []string
	var total int64
	err = filepath.WalkDir(work, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(work, p)
		if err != nil || rel == "." {
			return err
		}
		name := filepath.ToSlash(rel)
		if d.IsDir() {
			if d.Name() == ".git" || ignored.Match(strings.Split(name, "/"), true) {
				return filepath.SkipDir
			}
			return nil
		}
		if ignored.Match(strings.Split(name, "/"), false) {
			return nil
		}
		sum, size, ok, err := fileSum(p, d)
		if err != nil || !ok {
			return err
		}
		if was, sentIt := before[name]; !sentIt || was != sum {
			changed = append(changed, name)
			total += size
		}
		return nil
	})
	return changed, total, err
}

// fileSum is the sha256 of a regular file's bytes or a link's target, and
// a file's size; ok is false for anything else (a socket, a device).
func fileSum(p string, d fs.DirEntry) ([sha256.Size]byte, int64, bool, error) {
	var sum [sha256.Size]byte
	switch {
	case d.Type()&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		return sha256.Sum256([]byte(target)), 0, err == nil, err
	case d.Type().IsRegular():
		f, err := os.Open(p) //nolint:gosec // a file of the run's own tree
		if err != nil {
			return sum, 0, false, err
		}
		defer func() { _ = f.Close() }()
		hash := sha256.New()
		size, err := io.Copy(hash, f)
		copy(sum[:], hash.Sum(nil))
		return sum, size, err == nil, err
	}
	return sum, 0, false, nil
}

// emptyDir removes everything in dir, which stays: it may be a mount.
func emptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		errs = append(errs, os.RemoveAll(filepath.Join(dir, e.Name())))
	}
	return errors.Join(errs...)
}
