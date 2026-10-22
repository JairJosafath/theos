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
// itself), and uv can install the owner's Python packages from the host's
// index. A PyPI index on another host gets its own line, with the job's
// PyPI token, when there is one. Go reads it for https only: over plain
// http it fetches without credentials, which a public owner's packages allow.
// The file is the agent's own, mode 0600; one already there is replaced.
func WriteNetrc(home string, job Job) error {
	remote, err := url.Parse(job.RepoURL)
	if err != nil {
		return fmt.Errorf("repository URL %q: %w", job.RepoURL, err)
	}
	var netrc string
	if remote.Host != "" && job.Username != "" && job.Token != "" {
		// Go matches the machine against the URL's host, with its port.
		netrc = fmt.Sprintf("machine %s\nlogin %s\npassword %s\n", remote.Host, job.Username, job.Token)
	}
	if job.PyPIIndex != "" && job.PyPIToken != "" {
		index, err := url.Parse(job.PyPIIndex)
		if err != nil {
			return fmt.Errorf("PyPI index %q: %w", job.PyPIIndex, err)
		}
		if index.Host != "" && index.Host != remote.Host {
			netrc += fmt.Sprintf("machine %s\nlogin __token__\npassword %s\n", index.Host, job.PyPIToken)
		}
	}
	if netrc == "" {
		return nil
	}
	path := filepath.Join(home, ".netrc")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(netrc), 0o600)
}
