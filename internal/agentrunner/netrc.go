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

// WriteNetrc writes home/.netrc with the job's Git credentials for the Git
// host, so the go command can fetch modules from the host's package registry
// when an Agent's own GOPROXY names it (GoProxy sends the job's credentials
// itself), and uv, pip and requests can fetch packages from its PyPI index.
// A PyPI index on another host gets its own line, with the job's PyPI
// token, when there is one. Go reads it for https only: over plain http it
// fetches without credentials, which a public owner's packages allow.
//
// Go matches an entry against the URL's host with its port, while uv, pip,
// requests, curl and git over http match the hostname alone; when the host
// has a port the file holds one entry for each (#1710). The PyPI line names
// the index's hostname alone, as only uv reads it.
// The file is the agent's own, mode 0600; one already there is replaced.
func WriteNetrc(home string, job Job) error {
	remote, err := url.Parse(job.RepoURL)
	if err != nil {
		return fmt.Errorf("repository URL %q: %w", job.RepoURL, err)
	}
	var netrc strings.Builder
	if remote.Host != "" && job.Username != "" && job.Token != "" {
		machines := []string{remote.Host}
		if hostname := remote.Hostname(); hostname != remote.Host {
			machines = append(machines, hostname)
		}
		for _, machine := range machines {
			fmt.Fprintf(&netrc, "machine %s\nlogin %s\npassword %s\n", machine, job.Username, job.Token)
		}
	}
	if job.PyPIIndex != "" && job.PyPIToken != "" {
		index, err := url.Parse(job.PyPIIndex)
		if err != nil {
			return fmt.Errorf("PyPI index %q: %w", job.PyPIIndex, err)
		}
		if host := index.Hostname(); host != "" && host != remote.Hostname() {
			fmt.Fprintf(&netrc, "machine %s\nlogin __token__\npassword %s\n", host, job.PyPIToken)
		}
	}
	if netrc.Len() == 0 {
		return nil
	}
	path := filepath.Join(home, ".netrc")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(netrc.String()), 0o600)
}
