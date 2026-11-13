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

package ports

import "testing"

func TestSameHost(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"http://gitea:3000", "http://gitea:3000/templates/x", true},
		{"http://gitea:3000", "http://other:3000/templates/x", false},
		{"http://gitea:3000", "https://gitea:3000/templates/x", false},
		{"not a url", "http://gitea:3000", false},
	}
	for _, tt := range tests {
		if got := SameHost(tt.a, tt.b); got != tt.want {
			t.Errorf("SameHost(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestFileURL(t *testing.T) {
	for _, web := range []string{"http://gitea:3000/webshop/orders-api", "http://gitea:3000/webshop/orders-api/"} {
		repo := Repository{WebURL: web}
		if got, want := repo.FileURL("docs/specs/12.md"),
			"http://gitea:3000/webshop/orders-api/src/branch/main/docs/specs/12.md"; got != want {
			t.Errorf("FileURL() of %q = %q, want %q", web, got, want)
		}
	}
}
