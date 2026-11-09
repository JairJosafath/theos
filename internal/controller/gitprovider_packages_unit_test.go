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
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	c4v1beta1 "github.com/JairJosafath/theos/api/v1beta1"
	"github.com/JairJosafath/theos/internal/ports"
)

func TestPackagesOf(t *testing.T) {
	for _, tt := range []struct {
		name     string
		packages *c4v1beta1.PackagesSpec
		want     ports.Packages
		npm, py  string
	}{
		{name: "none leaves every registry to the host"},
		{name: "an empty packages too", packages: &c4v1beta1.PackagesSpec{}},
		{name: "npm alone", packages: &c4v1beta1.PackagesSpec{NPM: &c4v1beta1.NpmPackagesSpec{
			Scope: "@acme", Registry: "https://npm.example.com/", CredentialsSecretRef: &c4v1beta1.LocalRef{Name: "acme-npm"}}},
			want: ports.Packages{NpmScope: "@acme", NpmRegistry: "https://npm.example.com/"}, npm: "acme-npm"},
		{name: "pypi alone, without a Secret", packages: &c4v1beta1.PackagesSpec{PyPI: &c4v1beta1.PyPIPackagesSpec{
			Index: "https://pypi.example.com/simple", Upload: "https://pypi.example.com/"}},
			want: ports.Packages{PyPIIndex: "https://pypi.example.com/simple", PyPIUpload: "https://pypi.example.com/"}},
		{name: "both", packages: &c4v1beta1.PackagesSpec{
			NPM:  &c4v1beta1.NpmPackagesSpec{Scope: "@acme", CredentialsSecretRef: &c4v1beta1.LocalRef{Name: "acme-npm"}},
			PyPI: &c4v1beta1.PyPIPackagesSpec{Index: "https://pypi.example.com/simple", CredentialsSecretRef: &c4v1beta1.LocalRef{Name: "acme-pypi"}}},
			want: ports.Packages{NpmScope: "@acme", PyPIIndex: "https://pypi.example.com/simple"}, npm: "acme-npm", py: "acme-pypi"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gp := &c4v1beta1.GitProvider{}
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
			gp := &c4v1beta1.GitProvider{ObjectMeta: metav1.ObjectMeta{Namespace: "ns"}}
			gp.Spec.Packages = &c4v1beta1.PackagesSpec{NPM: &c4v1beta1.NpmPackagesSpec{Scope: "@acme"},
				PyPI: &c4v1beta1.PyPIPackagesSpec{Index: "https://pypi.example.com/simple"}}
			if tt.npm != "" {
				gp.Spec.Packages.NPM.CredentialsSecretRef = &c4v1beta1.LocalRef{Name: tt.npm}
			}
			if tt.pypi != "" {
				gp.Spec.Packages.PyPI.CredentialsSecretRef = &c4v1beta1.LocalRef{Name: tt.pypi}
			}
			got, err := packagesWithTokens(context.Background(), c, gp)
			if err != nil {
				t.Fatal(err)
			}
			want := ports.Packages{NpmScope: "@acme", NpmToken: tt.wantNpm,
				PyPIIndex: "https://pypi.example.com/simple", PyPIToken: tt.want}
			if got != want {
				t.Errorf("packagesWithTokens = %+v, want %+v", got, want)
			}
		})
	}
}
