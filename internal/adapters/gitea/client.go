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

// Package gitea is the Gitea adapter of the Git host's ports (internal/ports):
// it creates repositories, reads their history, pushes the initial commit and
// reaches Gitea's issues, Actions, agent access and package registry.
//
// It is an adapter: it may use net/http and go-git, and the business rules, but
// must not import Kubernetes or the api package; see docs/architecture.md.
package gitea

import (
	"fmt"
	"net/http"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// Client builds Providers. It is what the operator uses; tests substitute
// their own.
type Client struct {
	// HTTP is used for API calls. Defaults to a client with a 30 second timeout.
	HTTP *http.Client
}

// Provider returns the Provider for a GitProvider's settings.
func (c Client) Provider(cfg ports.Config) (ports.Provider, error) {
	switch cfg.Kind {
	case ports.KindGitea, "":
		return newGitea(cfg, c.httpClient())
	default:
		return nil, fmt.Errorf("git provider %q is not supported; v1 supports %q", cfg.Kind, ports.KindGitea)
	}
}

func (c Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}
