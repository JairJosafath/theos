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
	if !errors.Is(err, unreachable) || calls != 3 {
		t.Errorf("got %v after %d calls, want the error after 1 try and 2 retries", err, calls)
	}

	calls = 0
	rejected := errors.New("! [remote rejected] HEAD -> main (protected branch)")
	if err := retryTransient(context.Background(), delays, func() error { calls++; return rejected }); !errors.Is(err, rejected) || calls != 1 {
		t.Errorf("got %v after %d calls, want a rejection returned at once", err, calls)
	}
}
