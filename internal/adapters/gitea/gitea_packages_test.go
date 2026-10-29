package gitea

import (
	"testing"

	"github.com/JairJosafath/c4factory/internal/ports"
)

// TestRepositoryPackageRegistries checks the npm and PyPI settings a repository
// carries: the host's own registries and scope @c4factory by default, and the
// GitProvider's own when Config names them (#1229, decision 5 of
// docs/plans/done/app-packages.md).
func TestRepositoryPackageRegistries(t *testing.T) {
	for _, tt := range []struct {
		name     string
		packages ports.Packages
		want     ports.Repository
	}{
		{"the host's own by default", ports.Packages{}, ports.Repository{
			NpmScope:    "@c4factory",
			NpmRegistry: "https://git.example.com/api/packages/webshop/npm/",
			PyPIIndex:   "https://git.example.com/api/packages/webshop/pypi/simple",
			PyPIUpload:  "https://git.example.com/api/packages/webshop/pypi",
		}},
		{"the GitProvider's settings", ports.Packages{NpmScope: "@acme", NpmRegistry: "https://npm.example.com/",
			PyPIIndex: "https://pypi.example.com/simple", PyPIUpload: "https://pypi.example.com/upload"}, ports.Repository{
			NpmScope:    "@acme",
			NpmRegistry: "https://npm.example.com/",
			PyPIIndex:   "https://pypi.example.com/simple",
			PyPIUpload:  "https://pypi.example.com/upload",
		}},
		{"a registry without a trailing slash gets one (#1701)", ports.Packages{NpmRegistry: "https://npm.example.com"}, ports.Repository{
			NpmScope:    "@c4factory",
			NpmRegistry: "https://npm.example.com/",
			PyPIIndex:   "https://git.example.com/api/packages/webshop/pypi/simple",
			PyPIUpload:  "https://git.example.com/api/packages/webshop/pypi",
		}},
		{"each setting on its own", ports.Packages{NpmScope: "@acme"}, ports.Repository{
			NpmScope:    "@acme",
			NpmRegistry: "https://git.example.com/api/packages/webshop/npm/",
			PyPIIndex:   "https://git.example.com/api/packages/webshop/pypi/simple",
			PyPIUpload:  "https://git.example.com/api/packages/webshop/pypi",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Client{}.Provider(ports.Config{URL: "https://git.example.com/", Owner: "webshop", Packages: tt.packages})
			if err != nil {
				t.Fatal(err)
			}
			got := p.Repository("shop")
			if got.NpmScope != tt.want.NpmScope || got.NpmRegistry != tt.want.NpmRegistry ||
				got.PyPIIndex != tt.want.PyPIIndex || got.PyPIUpload != tt.want.PyPIUpload {
				t.Errorf("Repository(shop) registries:\n got %+v\nwant %+v", got, tt.want)
			}
			if got.GoProxy != "https://git.example.com/api/packages/webshop/go" {
				t.Errorf("GoProxy = %q", got.GoProxy)
			}
		})
	}
}

// TestSdistDir checks which top directory makes a tar's PKG-INFO an sdist's:
// <name>-<version>, in any spelling of the name and form of the version (#1701).
func TestSdistDir(t *testing.T) {
	for _, tt := range []struct {
		dir, name, version string
		want               bool
	}{
		{"shop_api-0.1.0", "shop-api", "0.1.0", true},
		{"shop-api-0.1.0", "shop-api", "0.1.0", true},
		{"Shop.API-0.1.0", "shop_api", "0.1.0", true},
		{"shop_api-1.0.0-RC.1", "shop-api", "1.0.0rc1", true},
		{"shop_api-1.0.0rc1", "shop-api", "1.0.0-RC.1", true},
		{"docs", "shop-api", "0.1.0", false},
		{"shop_api", "shop-api", "0.1.0", false},
		{"shop_api-0.2.0", "shop-api", "0.1.0", false},
		{"other-0.1.0", "shop-api", "0.1.0", false},
		{"shop_api-0.1.0", "shop-api", "not a version", false},
	} {
		if got := sdistDir(tt.dir, tt.name, tt.version); got != tt.want {
			t.Errorf("sdistDir(%q, %q, %q) = %v, want %v", tt.dir, tt.name, tt.version, got, tt.want)
		}
	}
}
