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
