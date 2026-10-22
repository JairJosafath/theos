package gitprovider

import "testing"

// TestNormalizePEP440 checks the normalisation a Python index applies to a
// version, the one a release tag is compared with (#1229).
func TestNormalizePEP440(t *testing.T) {
	for _, tt := range []struct {
		in, want string
		ok       bool
	}{
		{"1.0.0", "1.0.0", true},
		{"v1.0.0", "1.0.0", true},
		{"V1.0.0", "1.0.0", true},
		{"1.0.0-rc.1", "1.0.0rc1", true},
		{"v1.0.0-rc.1", "1.0.0rc1", true},
		{"1.0.0rc1", "1.0.0rc1", true},
		{"1.0.0RC1", "1.0.0rc1", true},
		{"1.0.0.c1", "1.0.0rc1", true},
		{"1.0.0pre1", "1.0.0rc1", true},
		{"1.0.0-preview-1", "1.0.0rc1", true},
		{"1.0.0alpha", "1.0.0a0", true},
		{"1.0.0_beta_2", "1.0.0b2", true},
		{"1.0.0a01", "1.0.0a1", true},
		{"1.0-1", "1.0.post1", true},
		{"1.0.post1", "1.0.post1", true},
		{"1.0-post", "1.0.post0", true},
		{"1.0rev2", "1.0.post2", true},
		{"1.0.r3", "1.0.post3", true},
		{"1.0.dev", "1.0.dev0", true},
		{"1.0-dev4", "1.0.dev4", true},
		{"1.0b2.post3.dev4", "1.0b2.post3.dev4", true},
		{"1!2.0", "1!2.0", true},
		{"0!2.0", "2.0", true},
		{"01.002.0003", "1.2.3", true},
		{"1.0+ubuntu-1", "1.0+ubuntu.1", true},
		{"1.0+Local_Build.007", "1.0+local.build.7", true},
		{"  1.0  ", "1.0", true},
		{"1.0.0-rc.1.2", "", false},
		{"1.0.0-SNAPSHOT", "", false},
		{"1.0.0+", "", false},
		{"", "", false},
		{"latest", "", false},
	} {
		got, ok := normalizePEP440(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("normalizePEP440(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestNormalizeProjectName(t *testing.T) {
	for in, want := range map[string]string{
		"shop":            "shop",
		"Shop_Core":       "shop-core",
		"shop.core":       "shop-core",
		"shop--core..api": "shop-core-api",
	} {
		if got := normalizeProjectName(in); got != want {
			t.Errorf("normalizeProjectName(%q) = %q, want %q", in, got, want)
		}
	}
}
