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

package github

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/JairJosafath/theos/internal/adapters/packages"
	"github.com/JairJosafath/theos/internal/ports"
)

// PublishReleasePackages publishes the package files of repo's release of
// tag: an npm tarball to repo.NpmRegistry, a wheel and an sdist to
// repo.PyPIUpload. It reads the release and brings the credentials;
// packages.Publisher, which the host adapters share, recognises and
// publishes the files, and its Publish says how. No release of tag is
// ErrNoRelease.
//
// On GitHub's own npm registry a package is linked to the repository its
// manifest names, so a manifest that names none is published naming repo.
// GitHub has no Python index: without one named by the GitProvider a wheel
// or an sdist is PackageInvalid, with that reason.
func (h *host) PublishReleasePackages(ctx context.Context, repo ports.Repository, tag string) (ports.ReleasePackages, error) {
	rel, err := h.GetRelease(ctx, repo, tag)
	if err != nil {
		return ports.ReleasePackages{Tag: tag}, err
	}
	if rel == nil {
		return ports.ReleasePackages{Tag: tag}, fmt.Errorf("%w: %s of %s/%s", ports.ErrNoRelease, tag, repo.Owner, repo.Name)
	}
	publisher := packages.Publisher{Name: "github", HTTP: h.http, Open: h.OpenReleaseAsset, Sign: h.signRegistry,
		LinkNpm: h.places.ownNpm}
	return publisher.Publish(ctx, repo, tag, rel.Assets)
}

// signRegistry signs req, a request to target, the npm registry or the
// Python index of ecosystem. The registry's own token, when the GitProvider
// names one, signs it. Else the operator's token does, for GitHub's own npm
// registry and nothing else: it is never sent to another host, and a
// Python index has no credential but its own.
func (h *host) signRegistry(req *http.Request, target *url.URL, ecosystem string) {
	switch ecosystem {
	case ports.EcosystemNpm:
		own, err := url.Parse(h.places.npm)
		switch {
		case h.packages.NpmToken != "":
			req.Header.Set("Authorization", "Bearer "+h.packages.NpmToken)
		case h.places.ownNpm && err == nil && target.Scheme == own.Scheme && strings.EqualFold(target.Host, own.Host):
			req.Header.Set("Authorization", "Bearer "+h.creds.Token)
		}
	case ports.EcosystemPyPI:
		if h.packages.PyPIToken != "" {
			req.SetBasicAuth("__token__", h.packages.PyPIToken)
		}
	}
}
