/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of C4 Factory, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/
package controller

import (
	"reflect"
	"testing"

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
