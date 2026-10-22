/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/
package controller

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1alpha1 "github.com/JairJosafath/c4factory/api/v1alpha1"
	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

func TestPackagesOf(t *testing.T) {
	for _, tt := range []struct {
		name     string
		packages *c4v1alpha1.PackagesSpec
		want     gitprovider.Packages
		npm, py  string
	}{
		{name: "none leaves every registry to the host"},
		{name: "an empty packages too", packages: &c4v1alpha1.PackagesSpec{}},
		{name: "npm alone", packages: &c4v1alpha1.PackagesSpec{NPM: &c4v1alpha1.NpmPackagesSpec{
			Scope: "@acme", Registry: "https://npm.example.com/", CredentialsSecretRef: &c4v1alpha1.LocalRef{Name: "acme-npm"}}},
			want: gitprovider.Packages{NpmScope: "@acme", NpmRegistry: "https://npm.example.com/"}, npm: "acme-npm"},
		{name: "pypi alone, without a Secret", packages: &c4v1alpha1.PackagesSpec{PyPI: &c4v1alpha1.PyPIPackagesSpec{
			Index: "https://pypi.example.com/simple", Upload: "https://pypi.example.com/"}},
			want: gitprovider.Packages{PyPIIndex: "https://pypi.example.com/simple", PyPIUpload: "https://pypi.example.com/"}},
		{name: "both", packages: &c4v1alpha1.PackagesSpec{
			NPM:  &c4v1alpha1.NpmPackagesSpec{Scope: "@acme", CredentialsSecretRef: &c4v1alpha1.LocalRef{Name: "acme-npm"}},
			PyPI: &c4v1alpha1.PyPIPackagesSpec{Index: "https://pypi.example.com/simple", CredentialsSecretRef: &c4v1alpha1.LocalRef{Name: "acme-pypi"}}},
			want: gitprovider.Packages{NpmScope: "@acme", PyPIIndex: "https://pypi.example.com/simple"}, npm: "acme-npm", py: "acme-pypi"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gp := &c4v1alpha1.GitProvider{}
			gp.Spec.Packages = tt.packages
			if got := packagesOf(gp); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("packagesOf = %+v, want %+v", got, tt.want)
			}
			if npm, py := packagesSecrets(gp); npm != tt.npm || py != tt.py {
				t.Errorf("packagesSecrets = %q, %q, want %q, %q", npm, py, tt.npm, tt.py)
			}
		})
	}
}

// The Crew publishes with each registry Secret's token, and with none when the
// Secret or its token key is missing (#1729).
func TestPackagesWithTokens(t *testing.T) {
	secret := func(name string, data map[string]string) *corev1.Secret {
		s := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: name}, Data: map[string][]byte{}}
		for k, v := range data {
			s.Data[k] = []byte(v)
		}
		return s
	}
	c := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(
		secret("acme-npm", map[string]string{"token": "npm-publish", "readToken": "npm-read"}),
		secret("acme-pypi", map[string]string{"readToken": "pypi-read"})).Build()
	for _, tt := range []struct {
		name          string
		npm, pypi     string
		wantNpm, want string
	}{
		{name: "no Secret named"},
		{name: "the token key publishes; a Secret without it publishes with none", npm: "acme-npm", pypi: "acme-pypi",
			wantNpm: "npm-publish"},
		{name: "a Secret that isn't there publishes with none", npm: "missing", pypi: "missing"},
		{name: "the npm Secret's token on PyPI too", pypi: "acme-npm", want: "npm-publish"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gp := &c4v1alpha1.GitProvider{ObjectMeta: metav1.ObjectMeta{Namespace: "ns"}}
			gp.Spec.Packages = &c4v1alpha1.PackagesSpec{NPM: &c4v1alpha1.NpmPackagesSpec{Scope: "@acme"},
				PyPI: &c4v1alpha1.PyPIPackagesSpec{Index: "https://pypi.example.com/simple"}}
			if tt.npm != "" {
				gp.Spec.Packages.NPM.CredentialsSecretRef = &c4v1alpha1.LocalRef{Name: tt.npm}
			}
			if tt.pypi != "" {
				gp.Spec.Packages.PyPI.CredentialsSecretRef = &c4v1alpha1.LocalRef{Name: tt.pypi}
			}
			got, err := packagesWithTokens(context.Background(), c, gp)
			if err != nil {
				t.Fatal(err)
			}
			want := gitprovider.Packages{NpmScope: "@acme", NpmToken: tt.wantNpm,
				PyPIIndex: "https://pypi.example.com/simple", PyPIToken: tt.want}
			if got != want {
				t.Errorf("packagesWithTokens = %+v, want %+v", got, want)
			}
		})
	}
}
