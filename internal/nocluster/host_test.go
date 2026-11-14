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

package nocluster_test

import (
	"context"
	"crypto/rand"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/nocluster"
	"github.com/JairJosafath/theos/internal/ports"
)

// commands are the commands that send the credential.
var commands = map[string]func(context.Context, nocluster.Env, *nocluster.Model) error{
	"scaffold": nocluster.Scaffold, "protect": nocluster.Protect, "status": nocluster.Status,
}

// #268: hosts compare after normalising: lower case, no trailing dot, the
// scheme's default port left out. A scheme, userinfo or port never makes
// another host the same one.
func TestHost268(t *testing.T) {
	for raw, want := range map[string]string{
		"git.example.com":                            "git.example.com",
		"https://git.example.com":                    "git.example.com",
		"HTTPS://Git.Example.COM:443/acme":           "git.example.com",
		"http://git.example.com:80":                  "git.example.com",
		"https://git.example.com./":                  "git.example.com",
		"git.example.com:443":                        "git.example.com",
		"https://git.example.com:3000":               "git.example.com:3000",
		"http://git.example.com:443":                 "git.example.com:443",
		"ssh://git.example.com:22/acme/x":            "git.example.com",
		"https://user:secret@git.example.com/acme":   "git.example.com",
		"https://git.example.com@other.example/acme": "other.example",
		"https://other.example#@git.example.com":     "other.example",
		"https://other.example?@git.example.com":     "other.example",
		"https://git.example.com.other.example":      "git.example.com.other.example",
		"http://[::1]:80":                            "::1",
		"http://[::1]:3000":                          "[::1]:3000",
	} {
		if got, err := nocluster.Host(raw); err != nil || got != want {
			t.Errorf("#268 Host(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"", "/srv/git/x.git", "https://", "https://other.example\\@git.example.com",
		"https://git.example.com\n.other.example", "http:/git.example.com", "HTTPS:/git.example.com:443"} {
		if got, err := nocluster.Host(raw); err == nil {
			t.Errorf("#268 Host(%q) = %q, want no host", raw, got)
		}
	}
}

// #268: a GitProvider whose url is on another host than the credential's
// is refused before any request, naming both hosts and THEOS_GIT_HOST: no
// GitHost is built, no template fetched, git credential fill never asked,
// and the token is in no error or output. The url forms that only look
// like the credential's host are refused the same way; one holding the
// credential's host as its user is refused for holding a user.
func TestOtherHostRefused268(t *testing.T) {
	for _, from := range []string{"THEOS_GIT_TOKEN", "git credential fill"} {
		for _, url := range []string{
			"https://git.example.org",
			"http://HOST@git.example.org",
			"http://git.example.org#@HOST",
			"http://NAME.git.example.org",
			"https://NAME",
		} {
			for name, command := range commands {
				t.Run(from+" "+url+" "+name, func(t *testing.T) {
					w := newWorld(t)
					host, _ := nocluster.Host(w.vars[nocluster.HostVar])
					hostname, _, _ := strings.Cut(host, ":")
					w.provider().Spec.URL = strings.NewReplacer("HOST", host, "NAME", hostname).Replace(url)
					helper := rand.Text()
					if from != nocluster.TokenVar {
						delete(w.vars, nocluster.TokenVar)
						w.run.out = "username=shop-owner\npassword=" + helper + "\n"
					}
					err := command(context.Background(), w.env, w.m)
					if err == nil {
						t.Fatalf("#268 %s ran with a GitProvider on another host", name)
					}
					says := []string{"is on ", "the credential is for " + host + ", from " + nocluster.HostVar,
						nocluster.HostVar + "="}
					if strings.Contains(url, "HOST@") {
						says = []string{"holds a user or password"}
					}
					for _, says := range says {
						if !strings.Contains(err.Error(), says) {
							t.Errorf("#268 the refusal does not say %q: %v", says, err)
						}
					}
					if len(w.hosts.configs) != 0 || len(w.templates.fetches) != 0 || len(w.run.calls) != 0 {
						t.Errorf("#268 before refusing: %d hosts built, %d templates fetched, ran %q",
							len(w.hosts.configs), len(w.templates.fetches), w.run.calls)
					}
					for _, secret := range []string{w.token, helper} {
						if strings.Contains(err.Error(), secret) || strings.Contains(w.out.String(), secret) {
							t.Error("#268 the refusal or the output carries the credential")
						}
					}
				})
			}
		}
	}
}

// #268: the credential's host comes from THEOS_GIT_HOST, else the origin
// remote; with neither, each command refuses before any request, saying to
// set THEOS_GIT_HOST, and git credential fill is never asked.
func TestNoCredentialHost268(t *testing.T) {
	for name, command := range commands {
		w := newWorld(t)
		delete(w.vars, nocluster.HostVar)
		delete(w.vars, nocluster.TokenVar)
		w.run.out = "password=" + w.token + "\n"
		err := command(context.Background(), w.env, w.m)
		if err == nil || !strings.Contains(err.Error(), "set "+nocluster.HostVar) ||
			strings.Contains(err.Error(), w.token) {
			t.Errorf("#268 %s with no host for the credential: %v", name, err)
		}
		if len(w.hosts.configs) != 0 || len(w.templates.fetches) != 0 || len(w.run.fills()) != 0 {
			t.Errorf("#268 %s before refusing: %d hosts built, %d templates fetched, ran %q", name,
				len(w.hosts.configs), len(w.templates.fetches), w.run.calls)
		}
		if want := []string{"git -C /project remote get-url origin\n"}; !slices.Equal(w.run.calls, want) {
			t.Errorf("#268 %s ran %q, want %q", name, w.run.calls, want)
		}
	}
}

// #268: THEOS_GIT_HOST naming the model's host, in another case or with the
// default port, or an origin remote on it, lets the scaffold through, and
// the credential reaches that host only; git credential fill is asked for
// that host alone.
func TestModelsHostScaffolds268(t *testing.T) {
	for name, set := range map[string]func(w *world, host string){
		"THEOS_GIT_HOST": func(*world, string) {},
		"THEOS_GIT_HOST in upper case": func(w *world, host string) {
			w.vars[nocluster.HostVar] = "HTTP://" + strings.ToUpper(host) + "/"
			w.provider().Spec.URL = strings.ToUpper(w.provider().Spec.URL)
		},
		"a GitProvider url with the default port and a trailing dot": func(w *world, host string) {
			hostname, _, _ := strings.Cut(host, ":")
			w.vars[nocluster.HostVar] = hostname
			w.provider().Spec.URL = "https://" + strings.ToUpper(hostname) + ".:443"
		},
		"the origin remote, with git credential fill": func(w *world, host string) {
			delete(w.vars, nocluster.HostVar)
			delete(w.vars, nocluster.TokenVar)
			w.run.origin = "http://deploy:" + rand.Text() + "@" + host + "/webshop/theos-webshop.git"
			w.run.out = "username=shop-owner\npassword=" + w.token + "\n"
		},
		"git credential fill": func(w *world, _ string) {
			delete(w.vars, nocluster.TokenVar)
			w.run.out = "username=shop-owner\npassword=" + w.token + "\n"
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			sample, err := nocluster.Host(w.provider().Spec.URL)
			if err != nil {
				t.Fatal(err)
			}
			set(w, sample)
			host, err := nocluster.Host(w.provider().Spec.URL)
			if err != nil {
				t.Fatal(err)
			}
			if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
				t.Fatalf("#268 %v", err)
			}
			if w.git.PushCount() != len(w.m.Containers) {
				t.Errorf("#268 %d pushes for %d Containers", w.git.PushCount(), len(w.m.Containers))
			}
			for _, cfg := range w.hosts.configs {
				if got, _ := nocluster.Host(cfg.URL); got != host || cfg.Credentials.Token != w.token {
					t.Errorf("#268 a host was built for %s with the credential %v", got, cfg.Credentials.Token == w.token)
				}
			}
			for _, fill := range w.run.fills() {
				if !strings.Contains(fill, "\nhost="+host+"\n") {
					t.Errorf("#268 git credential fill was asked %q", fill)
				}
			}
			if strings.Contains(w.out.String(), w.token) {
				t.Error("#268 the output carries the token")
			}
		})
	}
}

// #268: a template on another host than the credential's is fetched
// without it; one on the credential's host gets it.
func TestTemplateOnAnotherHost268(t *testing.T) {
	w := newWorld(t)
	host, _ := nocluster.Host(w.vars[nocluster.HostVar])
	moved := "https://templates.example.org/" + w.m.Objects.Frameworks[0].Name
	w.m.Objects.Frameworks[0].Spec.Template.URL = moved
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	var other, own int
	for _, f := range w.templates.fetches {
		got, _ := nocluster.Host(f.url)
		switch {
		case got != host && f.creds != nil:
			t.Errorf("#268 the template at %s was fetched with the credential", f.url)
		case got != host:
			other++
		case f.creds == nil || f.creds.Token != w.token:
			t.Errorf("#268 the template at %s on the credential's host was fetched without it", f.url)
		default:
			own++
		}
	}
	if other == 0 || own == 0 {
		t.Errorf("#268 %d fetches on another host and %d on the credential's", other, own)
	}

	// TemplatesFor drops a credential its caller hands for another host, or
	// for http where the credential goes over https only.
	token := rand.Text()
	for _, b := range []nocluster.Bound{{Host: "git.example.com"}, {Host: "git.example.com", HTTP: true}} {
		rec := &recordingTemplates{inner: templatesDir{t}}
		templates := nocluster.TemplatesFor(b, rec)
		withCredential := map[string]bool{"https://git.example.org/x": false,
			"https://git.example.com@git.example.org/x": false, "https://git.example.com.git.example.org/x": false,
			"https://GİT.example.com/x": false, "https://GIT.example.com:443/x": true,
			"http://git.example.com/x": b.HTTP, "ssh://git.example.com/x": false}
		for url := range withCredential {
			_, _ = templates.FetchTemplate(context.Background(), url, "v1", "", &ports.Credentials{Token: token})
		}
		for _, f := range rec.fetches {
			if want := withCredential[f.url]; (f.creds != nil) != want {
				t.Errorf("#268 %s fetched with the credential %v, want %v (http allowed: %v)", f.url, f.creds != nil,
					want, b.HTTP)
			}
		}
	}
}

// #268: scaffold fetches a template through TemplatesFor, which gives the
// credential only where Bound.Check lets it go: a template on the
// credential's host whose url holds a user is fetched without it, while
// the other templates on that host get it.
func TestScaffoldFetchesThroughTemplatesFor268(t *testing.T) {
	w := newWorld(t)
	host, _ := nocluster.Host(w.vars[nocluster.HostVar])
	withUser := "http://" + w.provider().Spec.Owner + "@" + host + "/templates/" + w.m.Objects.Frameworks[0].Name
	w.m.Objects.Frameworks[0].Spec.Template.URL = withUser
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatal(err)
	}
	var user, own int
	for _, f := range w.templates.fetches {
		switch {
		case f.url == withUser && f.creds != nil:
			t.Errorf("#268 the template at %s, whose url holds a user, was fetched with the credential", f.url)
		case f.url == withUser:
			user++
		case f.creds == nil || f.creds.Token != w.token:
			t.Errorf("#268 the template at %s on the credential's host was fetched without it", f.url)
		default:
			own++
		}
	}
	if user == 0 || own == 0 {
		t.Errorf("#268 %d fetches of the url with a user and %d of the others", user, own)
	}
}

// #268: CredentialHost reads THEOS_GIT_HOST before the origin remote, and
// refuses a THEOS_GIT_HOST with a user or password in it, or an origin
// remote naming no host, quoting neither.
func TestCredentialHost268(t *testing.T) {
	ctx := context.Background()
	secret := rand.Text()
	env := func(host string) func(string) string {
		return func(key string) string {
			if key == nocluster.HostVar {
				return host
			}
			return ""
		}
	}
	r := &runner{origin: "git@git.example.org:acme/theos-shop.git"}
	want := nocluster.Bound{Host: "git.example.com", From: nocluster.HostVar}
	if b, err := nocluster.CredentialHost(ctx, env("Git.Example.com:443"), r, "."); err != nil || b != want ||
		len(r.calls) != 0 {
		t.Errorf("#268 with %s set: %+v, %v, ran %q", nocluster.HostVar, b, err, r.calls)
	}
	want.HTTP = true
	if b, err := nocluster.CredentialHost(ctx, env("HTTP://git.example.com"), r, "."); err != nil || b != want {
		t.Errorf("#268 with %s set on http: %+v, %v", nocluster.HostVar, b, err)
	}
	want = nocluster.Bound{Host: "git.example.org", From: "the origin remote of /project"}
	if b, err := nocluster.CredentialHost(ctx, env(""), r, "/project"); err != nil || b != want {
		t.Errorf("#268 from an ssh origin remote: %+v, %v", b, err)
	}
	want.HTTP = true
	r = &runner{origin: "http://git.example.org/acme/theos-shop.git"}
	if b, err := nocluster.CredentialHost(ctx, env(""), r, "/project"); err != nil || b != want {
		t.Errorf("#268 from an http origin remote: %+v, %v", b, err)
	}
	for name, tc := range map[string]struct{ host, origin string }{
		"#268 THEOS_GIT_HOST with a password": {host: "https://deploy:" + secret + "@git.example.com"},
		"#268 THEOS_GIT_HOST naming no host":  {host: "https:///" + secret},
		// A scheme with one slash is a URL misspelt, not a host named http.
		"#268 THEOS_GIT_HOST with a scheme of one slash": {host: "http:/git.example.com"},
		"#268 an origin naming no host":                  {origin: "/srv/" + secret + ".git"},
		"#268 an origin naming no owner":                 {origin: "https://deploy:" + secret + "@git.example.com"},
	} {
		_, err := nocluster.CredentialHost(ctx, env(tc.host), &runner{origin: tc.origin}, ".")
		if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), nocluster.HostVar) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// git credential fill is asked for the credential's host only, and a
	// url on another host gets no credential at all.
	r = &runner{out: "password=" + secret + "\n"}
	b := nocluster.Bound{Host: "git.example.com", From: nocluster.HostVar}
	for _, url := range []string{"https://git.example.org", "http://git.example.com", "https://GİT.example.com",
		"https://u:" + secret + "@git.example.com"} {
		if _, err := nocluster.Credential(ctx, env(""), r, b, url); err == nil || len(r.calls) != 0 ||
			strings.Contains(err.Error(), secret) {
			t.Errorf("#268 a credential where it may not go: %v, ran %q", err, r.calls)
		}
	}
	creds, err := nocluster.Credential(ctx, env(""), r, b, "HTTPS://GIT.example.com:443")
	if want := []string{"git credential fill\nprotocol=https\nhost=git.example.com\n\n"}; err != nil ||
		creds.Token != secret || !slices.Equal(r.calls, want) {
		t.Errorf("#268 asked %q (%v), want %q", r.calls, err, want)
	}
}

// sentNothing fails the test when w built a GitHost, fetched a template or
// asked git credential fill, or when err or the output carries a secret.
func sentNothing(t *testing.T, w *world, err error, secrets ...string) {
	t.Helper()
	if len(w.hosts.configs) != 0 || len(w.templates.fetches) != 0 || len(w.run.fills()) != 0 {
		t.Errorf("#268 before refusing: %d hosts built, %d templates fetched, ran %q", len(w.hosts.configs),
			len(w.templates.fetches), w.run.calls)
	}
	for _, secret := range append(secrets, w.token) {
		if strings.Contains(err.Error(), secret) || strings.Contains(w.out.String(), secret) {
			t.Error("#268 the refusal or the output carries the credential")
		}
	}
}

// #268: a host outside ASCII is refused, as written or percent-encoded:
// its lower case and the name a resolver dials can differ. Its xn-- form
// is a host like any other.
func TestHostOutsideASCII268(t *testing.T) {
	for _, raw := range []string{"https://GİT.example.com", "https://Kitchen.example",
		"https://G%C4%B0T.example.com", "https://ｇit.example.com", "https://git.example.com。",
		"gİt.example.com"} {
		if got, err := nocluster.Host(raw); err == nil {
			t.Errorf("#268 Host(%q) = %q, want a refusal", raw, got)
		}
	}
	if got, err := nocluster.Host("https://xn--git-qyd.example.com"); err != nil || got != "xn--git-qyd.example.com" {
		t.Errorf("#268 an xn-- host: %q, %v", got, err)
	}
}

// #268: a GitProvider url whose host is outside ASCII, or a THEOS_GIT_HOST
// that is, is refused before any request, for THEOS_GIT_TOKEN and git
// credential fill alike: no GitHost built, no template fetched, git
// credential fill never asked.
func TestHostOutsideASCIIRefused268(t *testing.T) {
	for _, from := range []string{nocluster.TokenVar, "git credential fill"} {
		for _, form := range []string{"İ", "%C4%B0"} {
			for _, in := range []string{"url", nocluster.HostVar} {
				for name, command := range commands {
					t.Run(from+" "+form+" "+in+" "+name, func(t *testing.T) {
						w := newWorld(t)
						if in == nocluster.HostVar {
							w.vars[nocluster.HostVar] = strings.ReplaceAll(w.vars[nocluster.HostVar], "i", form)
						} else {
							w.provider().Spec.URL = strings.ReplaceAll(w.provider().Spec.URL, "i", form)
						}
						helper := rand.Text()
						if from != nocluster.TokenVar {
							delete(w.vars, nocluster.TokenVar)
							w.run.out = "username=shop-owner\npassword=" + helper + "\n"
						}
						err := command(context.Background(), w.env, w.m)
						if err == nil || !strings.Contains(err.Error(), "outside ASCII") {
							t.Fatalf("#268 %s with a host outside ASCII: %v", name, err)
						}
						sentNothing(t, w, err, helper)
					})
				}
			}
		}
	}
}

// #268: the credential goes over https unless THEOS_GIT_HOST, or the
// origin remote, says http, or the host is the machine itself. A
// GitProvider url on http without that, or on any other scheme, is refused
// before any request, naming THEOS_GIT_HOST=http:// as the way.
func TestSchemeOfTheCredential268(t *testing.T) {
	ctx := context.Background()
	for name, set := range map[string]func(w *world, host, hostname string){
		"#268 http with a bare THEOS_GIT_HOST": func(w *world, host, _ string) {
			w.vars[nocluster.HostVar] = host
		},
		"#268 http with THEOS_GIT_HOST on https": func(w *world, host, _ string) {
			w.vars[nocluster.HostVar] = "https://" + host
		},
		"#268 http with THEOS_GIT_HOST on ssh": func(w *world, host, _ string) {
			w.vars[nocluster.HostVar] = "ssh://" + host
		},
		"#268 http with an ssh origin remote": func(w *world, _, hostname string) {
			delete(w.vars, nocluster.HostVar)
			delete(w.vars, nocluster.TokenVar)
			w.run.origin = "ssh://git@" + hostname + "/webshop/theos-webshop.git"
			w.run.out = "password=" + w.token + "\n"
			w.provider().Spec.URL = "http://" + hostname
		},
	} {
		for command, run := range commands {
			w := newWorld(t)
			host, _ := nocluster.Host(w.provider().Spec.URL)
			hostname, _, _ := strings.Cut(host, ":")
			set(w, host, hostname)
			err := run(ctx, w.env, w.m)
			if err == nil || !strings.Contains(err.Error(), "https") ||
				!strings.Contains(err.Error(), nocluster.HostVar+"=http://") {
				t.Fatalf("%s: %s: %v", name, command, err)
			}
			sentNothing(t, w, err)
		}
	}
	for _, scheme := range []string{"ssh", "git", "ftp"} {
		w := newWorld(t)
		host, _ := nocluster.Host(w.provider().Spec.URL)
		w.provider().Spec.URL = scheme + "://" + host
		err := nocluster.Scaffold(ctx, w.env, w.m)
		if err == nil || !strings.Contains(err.Error(), "scheme") {
			t.Errorf("#268 a GitProvider url on %s: %v", scheme, err)
			continue
		}
		sentNothing(t, w, err)
	}
	for name, set := range map[string]func(w *world, host string){
		"#268 THEOS_GIT_HOST=http://": func(w *world, host string) { w.vars[nocluster.HostVar] = "http://" + host },
		"#268 an http origin remote": func(w *world, host string) {
			delete(w.vars, nocluster.HostVar)
			delete(w.vars, nocluster.TokenVar)
			w.run.origin = "http://" + host + "/webshop/theos-webshop.git"
			w.run.out = "password=" + w.token + "\n"
		},
		"#268 https with THEOS_GIT_HOST=http://": func(w *world, host string) {
			w.vars[nocluster.HostVar] = "http://" + host
			w.provider().Spec.URL = "https://" + host
		},
		"#268 the machine itself on http": func(w *world, _ string) {
			w.vars[nocluster.HostVar] = "127.0.0.1:3000"
			w.provider().Spec.URL = "http://127.0.0.1:3000"
		},
		"#268 localhost on http": func(w *world, _ string) {
			w.vars[nocluster.HostVar] = "localhost:3000"
			w.provider().Spec.URL = "http://localhost:3000"
		},
		"#268 ::1 on http": func(w *world, _ string) {
			w.vars[nocluster.HostVar] = "[::1]:3000"
			w.provider().Spec.URL = "http://[::1]:3000"
		},
	} {
		w := newWorld(t)
		host, _ := nocluster.Host(w.provider().Spec.URL)
		set(w, host)
		if err := nocluster.Scaffold(ctx, w.env, w.m); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		for _, fill := range w.run.fills() {
			if !strings.HasPrefix(fill, "git credential fill\nprotocol=http\n") {
				t.Errorf("%s: git credential fill was asked %q", name, fill)
			}
		}
	}

	// A template on http on the credential's host goes without the
	// credential unless http is allowed.
	w := newWorld(t)
	host, _ := nocluster.Host(w.provider().Spec.URL)
	w.vars[nocluster.HostVar] = host
	w.provider().Spec.URL = "https://" + host
	if err := nocluster.Scaffold(ctx, w.env, w.m); err != nil {
		t.Fatal(err)
	}
	for _, f := range w.templates.fetches {
		if strings.HasPrefix(f.url, "http://") && f.creds != nil {
			t.Errorf("#268 the template at %s went over http with the credential", f.url)
		}
	}
}

// #268: THEOS_GIT_TOKEN goes only to THEOS_GIT_HOST, which is set beside
// it: with the token and no THEOS_GIT_HOST each command refuses before any
// request, without reading the origin remote. Without the token, git
// credential fill is asked for the origin remote's host.
func TestTokenNeedsItsHost268(t *testing.T) {
	for name, command := range commands {
		w := newWorld(t)
		host, _ := nocluster.Host(w.provider().Spec.URL)
		delete(w.vars, nocluster.HostVar)
		w.run.origin = "http://" + host + "/webshop/theos-webshop.git"
		err := command(context.Background(), w.env, w.m)
		if err == nil || !strings.Contains(err.Error(), nocluster.TokenVar+" is sent only to "+nocluster.HostVar) {
			t.Fatalf("#268 %s with %s and no %s: %v", name, nocluster.TokenVar, nocluster.HostVar, err)
		}
		sentNothing(t, w, err)
		if len(w.run.calls) != 0 {
			t.Errorf("#268 %s ran %q", name, w.run.calls)
		}
	}
	w := newWorld(t)
	host, _ := nocluster.Host(w.provider().Spec.URL)
	delete(w.vars, nocluster.HostVar)
	delete(w.vars, nocluster.TokenVar)
	w.run.origin = "http://" + host + "/webshop/theos-webshop.git"
	w.run.out = "password=" + w.token + "\n"
	if err := nocluster.Scaffold(context.Background(), w.env, w.m); err != nil {
		t.Fatalf("#268 git credential fill for the origin remote's host: %v", err)
	}
	if fills := w.run.fills(); len(fills) == 0 || !strings.Contains(fills[0], "\nhost="+host+"\n") {
		t.Errorf("#268 git credential fill was asked %q", fills)
	}
}

// #268: a GitProvider url holding a user or password is refused before any
// request, quoting neither: a credential never sits in a model file.
func TestUserInfoInTheURLRefused268(t *testing.T) {
	for name, command := range commands {
		w := newWorld(t)
		secret := rand.Text()
		w.provider().Spec.URL = strings.Replace(w.provider().Spec.URL, "://", "://deploy:"+secret+"@", 1)
		err := command(context.Background(), w.env, w.m)
		if err == nil || !strings.Contains(err.Error(), "user or password") {
			t.Fatalf("#268 %s with a user and password in the url: %v", name, err)
		}
		sentNothing(t, w, err, secret)
	}
}

// #268: every GitProvider is checked before git credential fill is asked
// for any: a second GitProvider on another host refuses the command before
// the helper is asked for the first.
func TestEveryProviderCheckedFirst268(t *testing.T) {
	w := newWorld(t)
	delete(w.vars, nocluster.TokenVar)
	w.run.out = "password=" + w.token + "\n"
	other := *w.provider().DeepCopy()
	other.Name, other.Spec.URL = "elsewhere", "https://git.example.org"
	w.m.Objects.GitProviders = append(w.m.Objects.GitProviders, other)
	err := nocluster.Status(context.Background(), w.env, w.m)
	if err == nil || !strings.Contains(err.Error(), "GitProvider elsewhere") {
		t.Fatalf("#268 a second GitProvider on another host: %v", err)
	}
	sentNothing(t, w, err)
}
