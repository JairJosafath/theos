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

package gitea

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"

	"github.com/JairJosafath/theos/internal/adapters/packages"
	"github.com/JairJosafath/theos/internal/ports"
)

// adapterName starts the errors packages.Publisher makes, as the adapter's
// own do.
const adapterName = "gitea"

// PublishReleasePackages publishes the package files of repo's release of tag
// to the registries repo names: an npm tarball to repo.NpmRegistry, a wheel
// and an sdist to repo.PyPIUpload. It reads the release and brings its
// credentials; packages.Publisher, which the host adapters share, recognises
// and publishes the files, and its Publish says how. A release with no
// package file, an npm name outside repo.NpmScope, a version that isn't the
// tag's and a version the registry has already are outcomes, not errors; a
// repeat publishes nothing. No release of tag is ErrNoRelease.
func (g *gitea) PublishReleasePackages(ctx context.Context, repo ports.Repository, tag string) (ports.ReleasePackages, error) {
	out := ports.ReleasePackages{Tag: tag}
	rel, err := g.GetRelease(ctx, repo, tag)
	if err != nil {
		return out, err
	}
	if rel == nil {
		return out, fmt.Errorf("%w: %s of %s", ports.ErrNoRelease, tag, repoPath(repo))
	}
	publisher := packages.Publisher{Name: adapterName, HTTP: g.http, Open: g.OpenReleaseAsset, Sign: g.signRegistry}
	return publisher.Publish(ctx, repo, tag, rel.Assets)
}

// signRegistry signs req, a request to target, the npm registry or the Python
// index of ecosystem. The registry's token, when set, signs it; else the
// host's own credentials do, on the host's own registry only: they are never
// sent to another host. The registry is the host's own when target has the
// provider's scheme and host, each as it is written.
func (g *gitea) signRegistry(req *http.Request, target *url.URL, ecosystem string) {
	var token, authorization string
	switch ecosystem {
	case ports.EcosystemNpm:
		token = g.packages.NpmToken
		authorization = "Bearer " + token
	case ports.EcosystemPyPI:
		token = g.packages.PyPIToken
		authorization = "Basic " + base64.StdEncoding.EncodeToString([]byte("__token__:"+token))
	}
	switch {
	case token != "":
		req.Header.Set("Authorization", authorization)
	case target.Scheme == g.base.Scheme && target.Host == g.base.Host:
		if g.basic != nil {
			req.SetBasicAuth(g.basic.Username, g.basic.Token)
		} else if g.creds.Token != "" {
			req.Header.Set("Authorization", "token "+g.creds.Token)
		}
	}
}
