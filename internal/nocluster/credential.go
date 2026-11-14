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

package nocluster

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/JairJosafath/theos/internal/ports"
)

// TokenVar is the environment variable theos reads a Git host's token from
// before it asks git credential fill (decision 9).
const TokenVar = "THEOS_GIT_TOKEN" //nolint:gosec // the variable's name

// Runner runs a command with stdin and returns what it wrote to stdout.
// cmd/theos runs real commands; tests give a fake.
type Runner interface {
	Run(ctx context.Context, stdin, name string, args ...string) ([]byte, error)
}

// Credential is the credential theos reaches the GitProvider url hostURL
// with, where b, the one place the credential is for (CredentialHost),
// lets it go (Bound.Check): THEOS_GIT_TOKEN from getenv, else what git
// credential fill gives for b's host. A url the credential may not go to
// gets none, and git credential fill is asked for b's host only. The
// credential is never stored, and no error carries it. A token alone names
// no account: the host says whose it is (ports.GitHost.Login).
func Credential(ctx context.Context, getenv func(string) string, run Runner, b Bound, hostURL string) (
	ports.Credentials, error) {
	if err := b.Check(hostURL); err != nil {
		return ports.Credentials{}, err
	}
	if token := getenv(TokenVar); token != "" {
		return ports.Credentials{Token: token}, nil
	}
	u, err := url.Parse(hostURL)
	if err != nil {
		return ports.Credentials{}, errors.New("its url names no host")
	}
	host := b.Host
	out, err := run.Run(ctx, "protocol="+strings.ToLower(u.Scheme)+"\nhost="+host+"\n\n", "git", "credential", "fill")
	if err != nil {
		return ports.Credentials{}, fmt.Errorf("no credential for %s: %s is unset and git credential fill failed: %w",
			host, TokenVar, err)
	}
	var creds ports.Credentials
	lines := bufio.NewScanner(bytes.NewReader(out))
	for lines.Scan() {
		key, value, _ := strings.Cut(lines.Text(), "=")
		switch key {
		case "username":
			creds.Username = value
		case "password":
			creds.Token = value
		}
	}
	if creds.Token == "" {
		return ports.Credentials{}, errors.New("no credential for " + host + ": " + TokenVar +
			" is unset and git credential fill gave no password")
	}
	return creds, nil
}
