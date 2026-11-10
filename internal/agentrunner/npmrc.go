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

package agentrunner

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// WriteNpmrc writes home/.npmrc so npm sends the owner's scope to the
// owner's registry, and nowhere else, with the token the registry takes:
// the agent's Git token for the Git host's own registry, else the job's
// npm token; none when there is none (a public registry). Nothing is
// written without a registry. The file is the agent's own, mode 0600; one
// already there is replaced.
func WriteNpmrc(home string, job Job) error {
	if job.NpmRegistry == "" {
		return nil
	}
	registry, err := url.Parse(job.NpmRegistry)
	if err != nil {
		return fmt.Errorf("npm registry %q: %w", job.NpmRegistry, err)
	}
	if registry.Host == "" {
		return fmt.Errorf("npm registry %q has no host", job.NpmRegistry)
	}
	scope := job.NpmScope
	if scope == "" {
		scope = DefaultNpmScope
	}
	var npmrc strings.Builder
	fmt.Fprintf(&npmrc, "%s:registry=%s\n", scope, job.NpmRegistry)
	if token := job.registryToken(registry, job.NpmToken); token != "" {
		// npm matches the auth line against the registry URL's host and
		// path, scheme left off, up to the trailing slash.
		path := strings.TrimSuffix(registry.Path, "/") + "/"
		fmt.Fprintf(&npmrc, "//%s%s:_authToken=%s\n", registry.Host, path, token)
	}
	path := filepath.Join(home, ".npmrc")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(npmrc.String()), 0o600)
}

// DefaultNpmScope is the scope the factory's npm packages share unless the
// GitProvider names another.
const DefaultNpmScope = "@theos"

// registryToken is the token a package registry takes: the agent's Git
// token when the registry is on the Git host, else the registry's own.
func (job Job) registryToken(registry *url.URL, own string) string {
	if remote, err := url.Parse(job.RepoURL); err == nil && remote.Host != "" && remote.Host == registry.Host {
		return job.Token
	}
	return own
}
