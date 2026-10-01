package agentrunner

import (
	"context"
	"strings"
	"time"
)

// networkRetryDelays is how long the runner waits before each retry of a clone
// or push that failed to reach the Git host: about a minute in all, enough for
// a Git host or a busy API server on a small cluster to come back.
var networkRetryDelays = []time.Duration{5 * time.Second, 15 * time.Second, 40 * time.Second}

// transientMarkers are what git says when it couldn't reach the remote, as
// opposed to the remote refusing (a protected branch, a non-fast-forward push,
// bad credentials), which a retry won't change.
var transientMarkers = []string{
	"unable to access",
	"could not resolve host",
	"couldn't connect to server",
	"connection refused",
	"connection reset",
	"connection timed out",
	"operation timed out",
	"the remote end hung up unexpectedly",
	"rpc failed",
	"early eof",
}

// transientGitError reports whether a git error means the remote could not be
// reached, so trying again may work. Authentication failures say "unable to
// access" too, but a retry can't fix them.
func transientGitError(err error) bool {
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "authentication failed") || strings.Contains(msg, "not found") ||
		strings.Contains(msg, "rejected") || strings.Contains(msg, "403") {
		return false
	}
	for _, marker := range transientMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// retryTransient runs op, and again after each delay while it fails to reach
// the remote. A blip on the Git host would otherwise cost the Task a round.
func retryTransient(ctx context.Context, delays []time.Duration, op func() error) error {
	err := op()
	for _, delay := range delays {
		if err == nil || !transientGitError(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(delay):
		}
		err = op()
	}
	return err
}
