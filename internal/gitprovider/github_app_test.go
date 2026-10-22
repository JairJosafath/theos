package gitprovider

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeGitHubApp is GitHub's endpoint minting an installation token, for one
// App's installation. It answers a JWT the App's key signed with a new token
// each time, expiring an hour after its clock.
type fakeGitHubApp struct {
	appID, installation string
	public              *rsa.PublicKey
	now                 func() time.Time
	mints               int
}

func (f *fakeGitHubApp) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/app/installations/"+f.installation+"/access_tokens" {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		return
	}
	iss, err := verifyAppJWT(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), f.public, f.now())
	if err != nil || iss != f.appID {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"message": fmt.Sprintf("A JSON web token could not be decoded (iss %q, %v)", iss, err)})
		return
	}
	f.mints++
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{"token": fmt.Sprintf("ghs_minted%d", f.mints),
		"expires_at": f.now().Add(time.Hour).UTC().Format(time.RFC3339)})
}

// verifyAppJWT checks a GitHub App's JWT the way GitHub does: RS256 signed by
// public, issued before now and expiring after it, at most ten minutes on. It
// returns its issuer.
func verifyAppJWT(jwt string, public *rsa.PublicKey, now time.Time) (string, error) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return "", errors.New("not a JWT")
	}
	header, _ := base64.RawURLEncoding.DecodeString(parts[0])
	if string(header) != `{"alg":"RS256","typ":"JWT"}` {
		return "", fmt.Errorf("header %s", header)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(public, crypto.SHA256, digest[:], sig); err != nil {
		return "", err
	}
	payload, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims struct {
		IssuedAt  int64  `json:"iat"`
		ExpiresAt int64  `json:"exp"`
		Issuer    string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}
	if claims.IssuedAt > now.Unix() || claims.ExpiresAt <= now.Unix() || claims.ExpiresAt-claims.IssuedAt > 600 {
		return "", fmt.Errorf("iat %d, exp %d at %d", claims.IssuedAt, claims.ExpiresAt, now.Unix())
	}
	return claims.Issuer, nil
}

func appKey(t *testing.T, pkcs8 bool) (*rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	if !pkcs8 {
		return key, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func TestGitHubTokenMintsAndKeepsAnAppToken(t *testing.T) {
	for _, pkcs8 := range []bool{false, true} {
		t.Run(fmt.Sprintf("pkcs8=%v", pkcs8), func(t *testing.T) {
			key, pemKey := appKey(t, pkcs8)
			clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
			now := func() time.Time { return clock }
			app := &fakeGitHubApp{appID: "1234", installation: "5678", public: &key.PublicKey, now: now}
			server := httptest.NewServer(app)
			t.Cleanup(server.Close)
			client := Client{Now: now}
			secret := map[string][]byte{"appId": []byte("1234"), "installationId": []byte("5678"), "privateKey": pemKey}

			get := func() string {
				t.Helper()
				token, err := client.GitHubToken(context.Background(), server.URL, secret)
				if err != nil {
					t.Fatalf("GitHubToken() error = %v", err)
				}
				return token
			}
			if got := get(); got != "ghs_minted1" {
				t.Fatalf("first token = %q, want ghs_minted1", got)
			}
			clock = clock.Add(54 * time.Minute)
			if got := get(); got != "ghs_minted1" || app.mints != 1 {
				t.Fatalf("token 6 minutes before it expires = %q after %d mints, want the kept ghs_minted1", got, app.mints)
			}
			clock = clock.Add(time.Minute + time.Second)
			if got := get(); got != "ghs_minted2" {
				t.Fatalf("token under 5 minutes before it expires = %q, want a new ghs_minted2", got)
			}
		})
	}
}

func TestGitHubTokenRefusedMint(t *testing.T) {
	key, pemKey := appKey(t, false)
	other, _ := appKey(t, false)
	for _, tc := range []struct {
		name, appID, installation string
		privateKey                []byte
		public                    *rsa.PublicKey
		want                      string
	}{
		{"another App's key", "1234", "5678", pemKey, &other.PublicKey, "A JSON web token could not be decoded"},
		{"another App", "999", "5678", pemKey, &key.PublicKey, "A JSON web token could not be decoded"},
		{"no such installation", "1234", "4321", pemKey, &key.PublicKey, "Not Found"},
		{"installation not a number", "1234", "../x", pemKey, &key.PublicKey, `installationId "../x" is not a number`},
		{"no PEM", "1234", "5678", []byte("not a key"), &key.PublicKey, "privateKey: no PEM block"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := &fakeGitHubApp{appID: "1234", installation: "5678", public: tc.public, now: time.Now}
			server := httptest.NewServer(app)
			t.Cleanup(server.Close)
			secret := map[string][]byte{"appId": []byte(tc.appID), "installationId": []byte(tc.installation),
				"privateKey": tc.privateKey}
			_, err := Client{}.GitHubToken(context.Background(), server.URL, secret)
			if ReleaseReason(err) != ReasonUnauthorized {
				t.Fatalf("GitHubToken() error = %v, want %s", err, ReasonUnauthorized)
			}
			if !strings.Contains(err.Error(), "minting the GitHub App's installation token failed") ||
				!strings.Contains(err.Error(), tc.want) {
				t.Errorf("GitHubToken() error = %q, want GitHub's %q", err, tc.want)
			}
		})
	}
}

func TestGitHubTokenWithoutAnApp(t *testing.T) {
	_, pemKey := appKey(t, false)
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
	}))
	t.Cleanup(server.Close)
	for _, tc := range []struct {
		name   string
		secret map[string][]byte
		want   string
	}{
		{"token", map[string][]byte{"token": []byte(" ghp_x\n")}, "ghp_x"},
		{"token wins over an App", map[string][]byte{"token": []byte("ghp_x"), "appId": []byte("1"),
			"installationId": []byte("2"), "privateKey": pemKey}, "ghp_x"},
		{"an App without its key", map[string][]byte{"appId": []byte("1"), "installationId": []byte("2")}, ""},
		{"nothing", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Client{}.GitHubToken(context.Background(), server.URL, tc.secret)
			if err != nil || got != tc.want {
				t.Errorf("GitHubToken() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
