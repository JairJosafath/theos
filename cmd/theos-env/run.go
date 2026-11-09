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
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/JairJosafath/theos/internal/model"
)

// runMain runs a command in the environment the run leased as name, and
// exits with its exit code.
func runMain(args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	name, argv := args[0], args[1:]
	if argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return exitUsage
	}
	c := caller{name: name, url: getenv(model.TestEnvVar(name, model.TestEnvURL)),
		token: getenv(model.TestEnvVar(name, model.TestEnvToken)), ca: getenv(model.TestEnvVar(name, model.TestEnvCA)),
		max: model.TestEnvMaxTree, stdout: stdout, stderr: stderr}
	code, err := c.run(context.Background(), argv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "theos-env: %v\n", err)
		return exitFailed
	}
	return code
}

// caller is one run in an environment.
type caller struct {
	name, url, token, ca string
	// max is how many bytes of files it sends and takes back at most.
	max            int64
	stdout, stderr io.Writer
}

func (c *caller) run(ctx context.Context, argv []string) (int, error) {
	if c.url == "" || c.token == "" {
		return 0, fmt.Errorf("this run leased no environment %s: %s and %s are unset", c.name,
			model.TestEnvVar(c.name, model.TestEnvURL), model.TestEnvVar(c.name, model.TestEnvToken))
	}
	top, err := gitOut("rev-parse", "--show-toplevel")
	if err != nil {
		return 0, fmt.Errorf("run sends the git working tree it is called in: %w", err)
	}
	prefix, err := gitOut("rev-parse", "--show-prefix")
	if err != nil {
		return 0, err
	}
	root, err := os.OpenRoot(top)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()
	names, size, err := treeFiles(top, root)
	if err != nil {
		return 0, err
	}
	if size > c.max {
		return 0, fmt.Errorf("the tree's files are %d MiB, over the %d MiB a run sends: "+
			"ignore what the command doesn't need in .gitignore", size>>20, c.max>>20)
	}
	client, err := c.client()
	if err != nil {
		return 0, err
	}
	body, sending := io.Pipe()
	go func() { sending.CloseWithError(writeTree(sending, root, names)) }()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(c.url, "/")+"/run", body)
	if err != nil {
		return 0, err
	}
	command, _ := json.Marshal(argv)
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set(headerCommand, string(command))
	req.Header.Set(headerDir, strings.TrimSuffix(prefix, "/"))
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("reaching environment %s: %w", c.name, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("environment %s refused the run: %s: %s", c.name, resp.Status, errorBody(resp.Body))
	}
	return c.follow(resp.Body, root)
}

// follow copies the run's output as it comes and writes the files it
// brings back into the tree, until the exit code.
func (c *caller) follow(r io.Reader, root *os.Root) (int, error) {
	files, toFiles := io.Pipe()
	type result struct {
		written []treeFile
		err     error
	}
	got := make(chan result, 1)
	started := false
	start := func() {
		started = true
		go func() {
			written, err := readTree(files, root, c.max)
			if err != nil {
				_ = files.CloseWithError(err)
			}
			got <- result{written, err}
		}()
	}
	for {
		kind, payload, err := readFrame(r)
		if err != nil {
			_ = toFiles.CloseWithError(err)
			return 0, fmt.Errorf("environment %s ended the run before it finished: %w", c.name, err)
		}
		switch kind {
		case frameStdout:
			_, _ = c.stdout.Write(payload)
		case frameStderr:
			_, _ = c.stderr.Write(payload)
		case frameMessage:
			_, _ = fmt.Fprintf(c.stderr, "theos-env: %s: %s\n", c.name, payload)
		case frameFiles:
			if !started {
				start()
			}
			if _, err := toFiles.Write(payload); err != nil {
				return 0, fmt.Errorf("writing the files back: %w", err)
			}
		case frameExit:
			code, err := strconv.Atoi(string(payload))
			if err != nil {
				return 0, fmt.Errorf("environment %s sent the exit code %q", c.name, payload)
			}
			if started {
				_ = toFiles.Close()
				res := <-got
				if res.err != nil {
					return 0, fmt.Errorf("writing the files back: %w", res.err)
				}
				c.report(res.written)
			}
			return code, nil
		}
	}
}

// report says on stderr which files came back, at most 20 by name.
func (c *caller) report(written []treeFile) {
	names := make([]string, 0, min(len(written), 20))
	for _, f := range written[:min(len(written), 20)] {
		names = append(names, f.name)
	}
	more := ""
	if len(written) > len(names) {
		more = fmt.Sprintf(" and %d more", len(written)-len(names))
	}
	_, _ = fmt.Fprintf(c.stderr, "theos-env: %s changed or added %s%s\n", c.name, strings.Join(names, ", "), more)
}

// client is the HTTP client for the environment: one that trusts the
// environment's certificate alone when it has one. The certificate is
// pinned, not a host name: a remote machine's address comes after its
// certificate is made (model.TestEnvCertificate), so the server must show
// that certificate, or one it signed, whatever address it answers on.
func (c *caller) client() (*http.Client, error) {
	if c.ca == "" {
		return &http.Client{}, nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(c.ca)) {
		return nil, fmt.Errorf("%s holds no PEM certificate", model.TestEnvVar(c.name, model.TestEnvCA))
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12,
		InsecureSkipVerify: true, //nolint:gosec // VerifyConnection checks the chain against the pinned certificate
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return fmt.Errorf("environment %s sent no certificate", c.name)
			}
			intermediates := x509.NewCertPool()
			for _, cert := range cs.PeerCertificates[1:] {
				intermediates.AddCert(cert)
			}
			_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{Roots: pool, Intermediates: intermediates,
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
			if err != nil {
				return fmt.Errorf("environment %s's certificate isn't the one %s pins: %w", c.name,
					model.TestEnvVar(c.name, model.TestEnvCA), err)
			}
			return nil
		}}
	return &http.Client{Transport: transport}, nil
}

// treeFiles lists the working tree's tracked and untracked, not ignored,
// files, as slash paths under top, and sums their sizes.
func treeFiles(top string, root *os.Root) ([]string, int64, error) {
	cmd := exec.Command("git", "-C", top, "ls-files", "-z", "--cached", "--others", //nolint:gosec // the tree's own folder
		"--exclude-standard")
	out, err := cmd.Output()
	if err != nil {
		return nil, 0, fmt.Errorf("listing the tree's files: %w", err)
	}
	var names []string
	var size int64
	seen := map[string]bool{}
	for name := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		info, err := root.Lstat(filepath.FromSlash(name))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		names = append(names, name)
	}
	return names, size, nil
}

// gitOut runs git in the current folder and returns its output's first line.
func gitOut(args ...string) (string, error) {
	var stderr bytes.Buffer
	cmd := exec.Command("git", args...) //nolint:gosec // this file's fixed arguments
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out)), nil
}

// errorBody is what an answer that isn't a run says, trimmed.
func errorBody(r io.Reader) string {
	body, _ := io.ReadAll(io.LimitReader(r, 4096))
	return string(bytes.TrimSpace(body))
}
