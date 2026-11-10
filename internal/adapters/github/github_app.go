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
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JairJosafath/theos/internal/model"
	"github.com/JairJosafath/theos/internal/ports"
)

// appTokenMargin is how long before it expires an installation token is
// minted again.
const appTokenMargin = 5 * time.Minute

// appTokens keeps each installation's token for each repository until
// appTokenMargin before it expires, so every call of a target uses the one
// token. An expired entry goes when the next token is kept (#1792), so a
// rotated key or a deleted target leaves one behind for an hour at most.
var appTokens = struct {
	sync.Mutex
	byKey map[string]appToken
}{byKey: map[string]appToken{}}

type appToken struct {
	token   string
	expires time.Time
}

// GitHubToken returns the token a github Secret's data gives: its token as it
// is, else an installation token of the GitHub App its three keys name, minted
// at apiURL (github.com's API when "") for repository (owner/name) alone, and
// kept until five minutes before it expires. The token reaches no other
// repository of the installation (#1792), with the permissions the
// installation has: the plan names only the deploy member's (Contents), and
// a Brief's requests need Issues too, so the App's installation says which.
// It returns "" when the Secret holds neither whole. A mint GitHub refuses,
// or a private key that is no RSA key, is a ReleaseError of
// ReasonUnauthorized with GitHub's message.
func (c Client) GitHubToken(ctx context.Context, apiURL, repository string, secret map[string][]byte) (string, error) {
	token, app, ok := model.GitHubCredential(secret)
	if !ok || app == nil {
		return token, nil
	}
	api, err := githubURL(apiURL, GitHubAPIURL)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(app.PrivateKey)
	key := api.String() + "\x00" + app.AppID + "\x00" + app.InstallationID + "\x00" + hex.EncodeToString(sum[:]) +
		"\x00" + repository
	now := c.now()
	appTokens.Lock()
	kept, found := appTokens.byKey[key]
	appTokens.Unlock()
	if found && now.Before(kept.expires.Add(-appTokenMargin)) {
		return kept.token, nil
	}
	minted, err := c.mintAppToken(ctx, api.String(), app, repository, now)
	if err != nil {
		return "", err
	}
	appTokens.Lock()
	for k, t := range appTokens.byKey {
		if !now.Before(t.expires) {
			delete(appTokens.byKey, k)
		}
	}
	appTokens.byKey[key] = minted
	appTokens.Unlock()
	return minted.token, nil
}

func (c Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// mintAppToken signs a JWT of app with its private key and trades it for an
// installation token of repository (owner/name) alone.
func (c Client) mintAppToken(ctx context.Context, api string, app *model.GitHubApp, repository string,
	now time.Time) (appToken, error) {
	const what = "minting the GitHub App's installation token"
	refused := func(format string, a ...any) error {
		return &ports.ReleaseError{Reason: ports.ReasonUnauthorized, Message: "github: " + what + " failed: " + fmt.Sprintf(format, a...)}
	}
	if _, err := strconv.ParseInt(app.InstallationID, 10, 64); err != nil {
		return appToken{}, refused("installationId %q is not a number", app.InstallationID)
	}
	_, name, found := strings.Cut(repository, "/")
	if !found || name == "" {
		return appToken{}, refused("repository %q is not owner/name", repository)
	}
	// GitHub takes the repositories' names, under the installation's owner.
	body, err := json.Marshal(struct {
		Repositories []string `json:"repositories"`
	}{[]string{name}})
	if err != nil {
		return appToken{}, &ports.ReleaseError{Reason: ports.ReasonGitHubError, Message: "github: " + err.Error()}
	}
	key, err := appPrivateKey(app.PrivateKey)
	if err != nil {
		return appToken{}, refused("privateKey: %v", err)
	}
	jwt, err := AppJWT(app.AppID, key, now)
	if err != nil {
		return appToken{}, refused("signing the JWT: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		api+"/app/installations/"+app.InstallationID+"/access_tokens", bytes.NewReader(body))
	if err != nil {
		return appToken{}, &ports.ReleaseError{Reason: ports.ReasonGitHubError, Message: "github: " + err.Error()}
	}
	setGitHubHeaders(req, jwt)
	g := githubAPI{http: c.HTTP}
	if g.http == nil {
		g.http = c.httpClient()
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := g.send(req, http.StatusCreated, what, &out); err != nil {
		if re, ok := errors.AsType[*ports.ReleaseError](err); ok && re.Status == http.StatusNotFound {
			// No such installation, or not this App's: the credential is wrong.
			re.Reason = ports.ReasonUnauthorized
		}
		return appToken{}, err
	}
	if out.Token == "" {
		return appToken{}, &ports.ReleaseError{Reason: ports.ReasonGitHubError, Status: http.StatusCreated,
			Message: "github: " + what + ": the response has no token"}
	}
	if out.ExpiresAt.IsZero() {
		out.ExpiresAt = now.Add(time.Hour)
	}
	return appToken{token: out.Token, expires: out.ExpiresAt}, nil
}

// appPrivateKey reads a GitHub App's private key: a PEM block of a PKCS #1 or
// PKCS #8 RSA key.
func appPrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(bytes.TrimSpace(data))
	if block == nil {
		return nil, errors.New("no PEM block")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("not a PKCS #1 or PKCS #8 RSA key")
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA key")
	}
	return key, nil
}

// AppJWT is the RS256 JSON Web Token a GitHub App of appID authenticates
// with: issued a minute before now, against clock drift, and expiring nine
// minutes after it, under GitHub's ten.
func AppJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, err := json.Marshal(struct {
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		Issuer    string `json:"iss"`
	}{now.Add(-time.Minute).Unix(), now.Add(9 * time.Minute).Unix(), appID})
	if err != nil {
		return "", err
	}
	signed := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
