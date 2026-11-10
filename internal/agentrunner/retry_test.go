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
	"errors"
	"testing"
	"time"
)

func TestTransientGitError(t *testing.T) {
	tests := []struct {
		msg  string
		want bool
	}{
		{"git push: exit status 128: fatal: unable to access 'http://gitea.gitea.svc:3000/apps/x/': " +
			"Failed to connect to gitea.gitea.svc port 3000 after 130000 ms: Couldn't connect to server", true},
		{"git clone: exit status 128: fatal: unable to access 'http://gitea/x/': Could not resolve host: gitea", true},
		{"git push: exit status 128: fatal: the remote end hung up unexpectedly", true},
		{"git push: exit status 1: error: RPC failed; HTTP 502 curl 22 The requested URL returned error: 502", true},
		{"git push: exit status 1: remote: error: protected branch main ! [remote rejected] HEAD -> main", false},
		{"git push: exit status 1: ! [rejected] HEAD -> task/x (non-fast-forward)", false},
		{"git clone: exit status 128: fatal: Authentication failed for 'http://gitea/x/'", false},
		{"git clone: exit status 128: fatal: repository 'http://gitea/x/' not found", false},
	}
	for _, tt := range tests {
		if got := transientGitError(errors.New(tt.msg)); got != tt.want {
			t.Errorf("transientGitError(%q) = %v, want %v", tt.msg, got, tt.want)
		}
	}
}

func TestRetryTransient(t *testing.T) {
	delays := []time.Duration{time.Millisecond, time.Millisecond}
	unreachable := errors.New("fatal: unable to access 'http://gitea/x/': Couldn't connect to server")

	calls := 0
	err := retryTransient(context.Background(), delays, func() error {
		calls++
		if calls < 3 {
			return unreachable
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Errorf("got %v after %d calls, want success on the third", err, calls)
	}

	calls = 0
	err = retryTransient(context.Background(), delays, func() error {
		calls++
		return unreachable
	})
	if !errors.Is(err, unreachable) || !unreachableHost(err.Error()) || calls != 3 {
		t.Errorf("got %v after %d calls, want the host unreachable after 1 try and 2 retries", err, calls)
	}

	calls = 0
	rejected := errors.New("! [remote rejected] HEAD -> main (protected branch)")
	if err := retryTransient(context.Background(), delays, func() error { calls++; return rejected }); !errors.Is(err, rejected) || unreachableHost(err.Error()) || calls != 1 {
		t.Errorf("got %v after %d calls, want a rejection returned at once", err, calls)
	}
}

func TestNetworkRetryWaitsOutAGiteaRestart(t *testing.T) {
	// A Gitea pod restarting on a pulled image takes a few minutes.
	var total time.Duration
	for _, d := range networkRetryDelays {
		total += d
	}
	if total < 4*time.Minute || total > 6*time.Minute {
		t.Errorf("the retries wait %v in all, want about five minutes", total)
	}
}

func TestRunWithTheGitHostUnreachableIsInterrupted(t *testing.T) {
	delays := networkRetryDelays
	networkRetryDelays = []time.Duration{time.Millisecond}
	t.Cleanup(func() { networkRetryDelays = delays })

	// Nothing listens on port 1: the clone can't connect.
	got := Run(context.Background(), testJob(t, "http://127.0.0.1:1/apps/photo-editor.git", RoleImplement, 1),
		failingAgent{err: errors.New("never runs")}, t.TempDir())
	if !got.Interrupted || got.Unavailable || !unreachableHost(got.Error) {
		t.Errorf("Run() = %+v, want an interrupted run whose Git host stayed unreachable", got)
	}
}
