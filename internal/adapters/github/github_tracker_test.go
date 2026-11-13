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

package github

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// trackerGitHub is GitHub's issues API for fakeGitHubRepo, which takes
// token, beside the minting endpoint of app's installation, at one address.
func trackerGitHub(t *testing.T, token string, app *fakeGitHubApp) (*fakeGitHubIssues, string) {
	t.Helper()
	f := &fakeGitHubIssues{errorf: t.Errorf, token: token, lacks: map[string]string{},
		comments: map[int][]githubComment{}, members: map[string]map[string]string{}, nextID: 1000}
	mux := http.NewServeMux()
	mux.Handle("/app/", app)
	mux.HandleFunc("/", f.serve)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f, srv.URL
}

// TestTrackerReadsWithATokenOrAnApp is the client's half of #1601, which the
// Brief's envtest proved on this client before #154 put the Secret's keys
// behind ports.IssueTrackers: a releases entry's Secret with a token reads
// its repository's issues, and one with a GitHub App's three keys reads them
// with an installation token minted once and kept across trackers.
func TestTrackerReadsWithATokenOrAnApp(t *testing.T) {
	key, pemKey := appKey(t, false)
	app := &fakeGitHubApp{appID: "88", installation: "5001", public: &key.PublicKey, now: time.Now}
	// The App's first mint is the token the repository takes.
	f, url := trackerGitHub(t, "ghs_minted1", app)
	f.open("Dark theme", "ada", false)
	source := ports.TrackerSource{Tracker: model.TrackerGitHub, Address: url, Project: fakeGitHubRepo}
	ctx := context.Background()
	read := func(secret map[string][]byte) {
		t.Helper()
		tracker, err := Client{}.Tracker(ctx, source, secret)
		if err != nil {
			t.Fatalf("Tracker() error = %v", err)
		}
		list, err := tracker.ListOpenIssues(ctx, "")
		if err != nil || len(list.Issues) != 1 || list.Issues[0].Title != "Dark theme" {
			t.Fatalf("ListOpenIssues() = %+v, %v; want the one issue", list, err)
		}
		if _, ok := tracker.(ports.TeamMembers); !ok {
			t.Errorf("Tracker() = %T, want a ports.TeamMembers too", tracker)
		}
	}

	read(map[string][]byte{"token": []byte("ghs_minted1")})
	if app.mints != 0 {
		t.Errorf("a token's Secret minted %d tokens, want none", app.mints)
	}
	secret := map[string][]byte{"appId": []byte("88"), "installationId": []byte("5001"), "privateKey": pemKey}
	read(secret)
	read(secret)
	if app.mints != 1 {
		t.Errorf("an App's Secret minted %d tokens over two reads, want 1: the token is kept", app.mints)
	}
}

// TestTrackerWithoutACredential: a Secret with no token and not the App's
// three keys, or none at all, says which keys it takes and calls nobody.
func TestTrackerWithoutACredential(t *testing.T) {
	_, pemKey := appKey(t, false)
	for name, secret := range map[string]map[string][]byte{
		"no Secret":   nil,
		"no keys":     {},
		"blank token": {"token": []byte("  ")},
		"half an App": {"appId": []byte("88"), "privateKey": pemKey},
	} {
		t.Run(name, func(t *testing.T) {
			tracker, err := Client{}.Tracker(context.Background(),
				ports.TrackerSource{Tracker: model.TrackerGitHub, Address: "http://127.0.0.1:1", Project: fakeGitHubRepo},
				secret)
			missing, ok := errors.AsType[*ports.CredentialMissingError](err)
			if !ok || tracker != nil {
				t.Fatalf("Tracker() = %v, %v; want a CredentialMissingError", tracker, err)
			}
			want := "the key token (or a GitHub App's appId, installationId, privateKey)"
			if missing.Keys != want {
				t.Errorf("Keys = %q, want %q", missing.Keys, want)
			}
		})
	}
}

// TestTrackerRefusedMint: a mint GitHub refuses is the port's error, with
// the reason, status and message the mint failed with.
func TestTrackerRefusedMint(t *testing.T) {
	key, pemKey := appKey(t, false)
	app := &fakeGitHubApp{appID: "88", installation: "5001", public: &key.PublicKey, now: time.Now}
	_, url := trackerGitHub(t, rand.Text(), app)
	secret := map[string][]byte{"appId": []byte("99"), "installationId": []byte("5001"), "privateKey": pemKey}
	_, err := Client{}.Tracker(context.Background(),
		ports.TrackerSource{Tracker: model.TrackerGitHub, Address: url, Project: fakeGitHubRepo}, secret)
	failed, ok := errors.AsType[*ports.IssuesError](err)
	if !ok || failed.Reason != ports.ReasonUnauthorized || failed.Status != http.StatusUnauthorized ||
		!strings.Contains(err.Error(), "minting the GitHub App's installation token failed") {
		t.Fatalf("Tracker() error = %#v, want an IssuesError of %s, HTTP 401, from the mint", err,
			ports.ReasonUnauthorized)
	}
}
