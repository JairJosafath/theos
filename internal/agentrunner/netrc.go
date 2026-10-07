package agentrunner

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// WriteNetrc writes home/.netrc with the job's Git credentials for the Git
// host, so the go command can fetch modules from the host's package registry
// (GOPROXY in the pod's environment). Go reads it for https only: over plain
// http it fetches without credentials, which a public owner's packages allow.
// The file is the agent's own, mode 0600; one already there is replaced.
func WriteNetrc(home string, job Job) error {
	remote, err := url.Parse(job.RepoURL)
	if err != nil {
		return fmt.Errorf("repository URL %q: %w", job.RepoURL, err)
	}
	if remote.Host == "" || job.Username == "" || job.Token == "" {
		return nil
	}
	// Go matches the machine against the URL's host, with its port.
	netrc := fmt.Sprintf("machine %s\nlogin %s\npassword %s\n", remote.Host, job.Username, job.Token)
	path := filepath.Join(home, ".netrc")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(netrc), 0o600)
}
