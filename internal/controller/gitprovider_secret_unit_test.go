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

package controller

import (
	"context"
	"crypto/rand"
	"maps"
	"slices"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/ports"
)

// TestProviderConfigCarriesTheSecret covers #81: a GitProvider's adapter is
// built with the whole of the provider's Secret. The user name and the
// token are who the operator acts as; every other key reaches the adapter
// by its name, which is how a host that mints its agents' credentials from
// an App gets the App. And a key that names a credential is one the
// release copy must never publish.
func TestProviderConfigCarriesTheSecret(t *testing.T) {
	scheme := briefScheme(t)
	token, key := rand.Text(), rand.Text()
	gp := &c4v1beta1.GitProvider{ObjectMeta: objectMeta("apps", "git"), Spec: c4v1beta1.GitProviderSpec{
		Provider: "elsewhere", URL: "https://git.example.com", Owner: "acme",
		CredentialsSecretRef: c4v1beta1.LocalRef{Name: "git-credentials"},
		Packages:             &c4v1beta1.PackagesSpec{NPM: &c4v1beta1.NpmPackagesSpec{Scope: "@acme"}}}}
	secret := &corev1.Secret{ObjectMeta: objectMeta("apps", "git-credentials"), Data: map[string][]byte{
		"username": []byte("ada"), "token": []byte(token), "appId": []byte("1234"), "privateKey": []byte(key)}}
	container := newContainer("apps", "orders-api", "webshop-core", c4v1beta1.ContainerTypeService)
	container.Spec.GitProviderRef.Name = gp.Name
	apiServer := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gp, secret, container).Build()
	ctx := context.Background()

	read, found, err := readCredentials(ctx, apiServer, gp)
	if err != nil || !found {
		t.Fatalf("readCredentials() found %v, %v; want the Secret", found, err)
	}
	cfg := providerConfig(gp, read, packagesOf(gp))
	if cfg.Kind != "elsewhere" || cfg.URL != gp.Spec.URL || cfg.Owner != "acme" || cfg.Packages.NpmScope != "@acme" {
		t.Errorf("the config is of %s at %s for %s with the scope %q, want the GitProvider's own", cfg.Kind, cfg.URL,
			cfg.Owner, cfg.Packages.NpmScope)
	}
	if want := (ports.Credentials{Username: "ada", Token: token}); cfg.Credentials != want {
		t.Error("the config's credentials are not the Secret's user name and token")
	}
	if got := slices.Sorted(maps.Keys(cfg.Secret)); !slices.Equal(got, []string{"appId", "privateKey"}) {
		t.Errorf("the adapter is handed the keys %q of the Secret, want appId and privateKey", got)
	}
	if string(cfg.Secret["appId"]) != "1234" || string(cfg.Secret["privateKey"]) != key {
		t.Error("the adapter is handed other values than the Secret holds")
	}

	bare := &corev1.Secret{ObjectMeta: objectMeta("apps", "git-credentials"),
		Data: map[string][]byte{"username": []byte("ada"), "token": []byte(token)}}
	plain := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gp, bare).Build()
	if read, _, err := readCredentials(ctx, plain, gp); err != nil || read.Rest != nil {
		t.Errorf("a Secret with a user name and a token alone gave the rest %v, %v; want none", read.Rest, err)
	}

	known, err := knownCredentials(ctx, apiServer, apiServer, container)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(known, token) || !slices.Contains(known, key) {
		t.Error("the credentials no release may hold lack the provider's token or its private key")
	}
	if slices.Contains(known, "1234") || slices.Contains(known, "ada") {
		t.Errorf("the credentials no release may hold name a setting: %d values, among them the App's id or the user name",
			len(known))
	}
}
