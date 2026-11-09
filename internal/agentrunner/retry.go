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
	"context"
	"fmt"
	"strings"
	"time"
)

// networkRetryDelays is how long the runner waits before each retry of a clone
// or push that failed to reach the Git host: about five minutes in all, enough
// for a Gitea pod to restart on a pulled image during an upgrade.
var networkRetryDelays = []time.Duration{
	5 * time.Second, 15 * time.Second, 40 * time.Second, 60 * time.Second, 90 * time.Second, 90 * time.Second,
}

// hostUnreachable starts the error of a clone or push whose Git host stayed
// unreachable through every retry. Run reports such a run interrupted: the
// infrastructure failed it, not the agent.
const hostUnreachable = "the Git host stayed unreachable"

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
// the remote. A blip on the Git host would otherwise cost the Task a round. When
// the remote is still unreachable after the last delay, the error says so with
// hostUnreachable.
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
	if err != nil && transientGitError(err) {
		return fmt.Errorf("%s: %w", hostUnreachable, err)
	}
	return err
}

// unreachableHost reports whether a run failed because its Git host stayed
// unreachable.
func unreachableHost(runError string) bool {
	return strings.Contains(runError, hostUnreachable)
}
