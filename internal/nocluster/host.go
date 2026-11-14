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
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/modelfacts"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// HostVar names the one Git host the credential is sent to (#268). Unset,
// the host is the project repository's origin remote's. Both are set
// outside the project repository's files, which anyone who changes the
// model can change.
const HostVar = "THEOS_GIT_HOST"

// defaultPorts are the ports a URL of each scheme names by leaving its port out.
var defaultPorts = map[string]string{schemeHTTP: "80", schemeHTTPS: "443", "ssh": "22"}

// schemeHTTPS is the scheme a bare host and an SSH remote's host are taken
// as; schemeHTTP is the one the credential goes over only where the
// person's setting says so (Bound).
const (
	schemeHTTPS = "https"
	schemeHTTP  = "http"
)

// Host is the host a URL names, or a bare host[:port] taken as https's,
// in the one form two hosts are compared in: lower case, no trailing dot,
// the scheme's default port left out. A URL's userinfo, path and query are
// not part of its host. A host outside ASCII, as written or
// percent-encoded, is refused: its lower case and the name a resolver
// dials can differ, so theos compares only the xn-- form. A value naming
// no host is an error, which never quotes the value: it may carry a
// credential.
func Host(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if !strings.Contains(s, "://") {
		// A scheme with one slash, such as http:/host, is a URL misspelt, not
		// a bare host named http.
		if strings.Contains(s, ":/") {
			return "", errors.New("names no host")
		}
		s = schemeHTTPS + "://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("names no host")
	}
	for _, b := range []byte(u.Hostname()) {
		if b > unicode.MaxASCII {
			return "", errors.New("names a host outside ASCII; write its xn-- form")
		}
	}
	hostname := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	port := u.Port()
	if port == "" || port == defaultPorts[strings.ToLower(u.Scheme)] {
		return hostname, nil
	}
	return net.JoinHostPort(hostname, port), nil
}

// Bound is the one place the credential is for (#268): the host, whether
// it may go over http, and where the person set it. CredentialHost reads
// it from outside the project repository's files.
type Bound struct {
	// Host is the host, in Host's form.
	Host string
	// HTTP is set when the person's own setting says http: THEOS_GIT_HOST
	// spelling http://, or an http origin remote. Without it the
	// credential goes over https only, unless Host is the machine itself.
	HTTP bool
	// From says where Host came from, for the refusals: THEOS_GIT_HOST, or
	// the origin remote of the -C directory.
	From string
}

// overHTTP reports whether the credential may go to b over http: the
// person's setting says http, or the host is the machine itself.
func (b Bound) overHTTP() bool {
	hostname := b.Host
	if h, _, err := net.SplitHostPort(b.Host); err == nil {
		hostname = h
	}
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return b.HTTP || ip != nil && ip.IsLoopback()
}

// CredentialHost is the one place the credential is for: THEOS_GIT_HOST
// from getenv, else the host of the origin remote of the project
// repository at dir, read through run. THEOS_GIT_TOKEN, a token for no
// host in particular, needs THEOS_GIT_HOST beside it; the origin remote
// names the host git credential fill is asked for only. The credential
// goes over http only when THEOS_GIT_HOST or the origin remote says http,
// or to the machine itself. Nothing in the model sets or widens it. With
// no host, it refuses, before any request, saying to set THEOS_GIT_HOST.
func CredentialHost(ctx context.Context, getenv func(string) string, run Runner, dir string) (Bound, error) {
	if set := getenv(HostVar); set != "" {
		if strings.Contains(set, "@") {
			return Bound{}, fmt.Errorf("%s holds a user or password; it names the Git host only", HostVar)
		}
		host, err := Host(set)
		if err != nil {
			return Bound{}, fmt.Errorf("%s %w; set it to the Git host the model is on, such as git.example.com",
				HostVar, err)
		}
		scheme, _, spelt := strings.Cut(strings.TrimSpace(set), "://")
		return Bound{Host: host, HTTP: spelt && strings.EqualFold(scheme, schemeHTTP), From: HostVar}, nil
	}
	if getenv(TokenVar) != "" {
		return Bound{}, fmt.Errorf("%s is sent only to %s: set %s to the host the token is for", TokenVar, HostVar,
			HostVar)
	}
	out, err := run.Run(ctx, "", "git", "-C", dir, "remote", "get-url", "origin")
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return Bound{}, fmt.Errorf("no Git host to send the credential to: set %s to the host the model is on, or "+
			"run theos in a project repository whose origin remote is on it", HostVar)
	}
	scheme, hostname, _, err := parseRemote(strings.TrimSpace(string(out)))
	if err != nil {
		return Bound{}, fmt.Errorf("reading the origin remote: %w; set %s to the Git host the model is on", err,
			HostVar)
	}
	host, err := Host(scheme + "://" + hostname)
	if err != nil {
		return Bound{}, fmt.Errorf("the origin remote %w; set %s to the Git host the model is on", err, HostVar)
	}
	return Bound{Host: host, HTTP: scheme == schemeHTTP, From: "the origin remote of " + dir}, nil
}

// CheckHosts refuses, before any request and before git credential fill is
// asked for any of them, a GitProvider whose url the credential may not
// go to (Bound.Check).
func CheckHosts(b Bound, providers []c4v1beta1.GitProvider) error {
	for _, gp := range providers {
		if err := b.Check(gp.Spec.URL); err != nil {
			return fmt.Errorf("GitProvider %s: %w", gp.Name, err)
		}
	}
	return nil
}

// Check refuses a url the credential may not go to: one holding a user or
// password, one on another host than b's (naming both hosts, where b's
// came from, and that a model on another host sets THEOS_GIT_HOST to it),
// and one on a scheme other than https, or http where b allows it. The
// refusal never quotes the url.
func (b Bound) Check(hostURL string) error {
	u, err := url.Parse(hostURL)
	if err != nil || !strings.Contains(hostURL, "://") {
		return errors.New("its url names no host")
	}
	if u.User != nil {
		return errors.New("its url holds a user or password; theos reads the credential from " + TokenVar +
			" or git credential fill, never from a model file")
	}
	got, err := Host(hostURL)
	if err != nil {
		return fmt.Errorf("its url %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if got != b.Host {
		set := got
		if scheme == schemeHTTP {
			set = schemeHTTP + "://" + got
		}
		return fmt.Errorf("its url is on %s, and the credential is for %s, from %s; theos sends the credential "+
			"only to the host it is for. If the model is on %s, set %s=%s", got, b.Host, b.From, got, HostVar, set)
	}
	switch {
	case scheme == schemeHTTPS, scheme == schemeHTTP && b.overHTTP():
		return nil
	case scheme == schemeHTTP:
		return fmt.Errorf("its url is on http, and the credential goes to %s over https only unless %s says "+
			"http; if %s serves http only, set %s=http://%s", b.Host, HostVar, b.Host, HostVar, b.Host)
	default:
		return fmt.Errorf("its url's scheme is %s; theos reaches a Git host over https, or http where %s says so",
			scheme, HostVar)
	}
}

// TemplatesFor gives a template fetch the credential only where it may go
// (Bound.Check): a template on any other host, or on http where b allows
// https only, pinned and public, is fetched without it.
func TemplatesFor(b Bound, fetcher modelfacts.TemplateFetcher) modelfacts.TemplateFetcher {
	return hostTemplates{bound: b, fetcher: fetcher}
}

type hostTemplates struct {
	bound   Bound
	fetcher modelfacts.TemplateFetcher
}

func (t hostTemplates) FetchTemplate(ctx context.Context, templateURL, ref, dir string,
	creds *ports.Credentials) (scaffold.Tree, error) {
	if t.bound.Check(templateURL) != nil {
		creds = nil
	}
	return t.fetcher.FetchTemplate(ctx, templateURL, ref, dir, creds)
}
