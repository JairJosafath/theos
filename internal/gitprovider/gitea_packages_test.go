package gitprovider

import "testing"

// TestRepositoryPackageRegistries checks the npm and PyPI settings a repository
// carries: the host's own registries and scope @c4factory by default, and the
// GitProvider's own when Config names them (#1229, decision 5 of
// docs/plans/done/app-packages.md).
func TestRepositoryPackageRegistries(t *testing.T) {
	for _, tt := range []struct {
		name     string
		packages Packages
		want     Repository
	}{
		{"the host's own by default", Packages{}, Repository{
			NpmScope:    "@c4factory",
			NpmRegistry: "https://git.example.com/api/packages/webshop/npm/",
			PyPIIndex:   "https://git.example.com/api/packages/webshop/pypi/simple",
			PyPIUpload:  "https://git.example.com/api/packages/webshop/pypi",
		}},
		{"the GitProvider's settings", Packages{NpmScope: "@acme", NpmRegistry: "https://npm.example.com/",
			PyPIIndex: "https://pypi.example.com/simple", PyPIUpload: "https://pypi.example.com/upload"}, Repository{
			NpmScope:    "@acme",
			NpmRegistry: "https://npm.example.com/",
			PyPIIndex:   "https://pypi.example.com/simple",
			PyPIUpload:  "https://pypi.example.com/upload",
		}},
		{"each setting on its own", Packages{NpmScope: "@acme"}, Repository{
			NpmScope:    "@acme",
			NpmRegistry: "https://git.example.com/api/packages/webshop/npm/",
			PyPIIndex:   "https://git.example.com/api/packages/webshop/pypi/simple",
			PyPIUpload:  "https://git.example.com/api/packages/webshop/pypi",
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Client{}.Provider(Config{URL: "https://git.example.com/", Owner: "webshop", Packages: tt.packages})
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
