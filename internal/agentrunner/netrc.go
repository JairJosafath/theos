package agentrunner

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// WriteNetrc writes home/.netrc with the job's Git credentials for the Git
// host, so the go command can fetch modules from the host's package registry
// when an Agent's own GOPROXY names it (GoProxy sends the job's credentials
// itself), and uv, pip and requests can fetch packages from its PyPI index.
// Go reads it for https only: over plain http it fetches without
// credentials, which a public owner's packages allow.
//
// Go matches an entry against the URL's host with its port, while uv, pip,
// requests, curl and git over http match the hostname alone; when the host
// has a port the file holds one entry for each (#1710).
// The file is the agent's own, mode 0600; one already there is replaced.
func WriteNetrc(home string, job Job) error {
	remote, err := url.Parse(job.RepoURL)
	if err != nil {
		return fmt.Errorf("repository URL %q: %w", job.RepoURL, err)
	}
	if remote.Host == "" || job.Username == "" || job.Token == "" {
		return nil
	}
	machines := []string{remote.Host}
	if hostname := remote.Hostname(); hostname != remote.Host {
		machines = append(machines, hostname)
	}
	var netrc string
	for _, machine := range machines {
		netrc += fmt.Sprintf("machine %s\nlogin %s\npassword %s\n", machine, job.Username, job.Token)
	}
	path := filepath.Join(home, ".netrc")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(netrc), 0o600)
}
