package agentrunner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteNetrc(t *testing.T) {
	for _, tt := range []struct {
		name, repoURL, want string
	}{
		{"an in-cluster Gitea, with its port", "http://gitea.gitea.svc.cluster.local:3000/apps/image-shrink.git",
			"machine gitea.gitea.svc.cluster.local:3000\nlogin image-shrink-agent\npassword " + gitToken + "\n"},
		{"an external Gitea", "https://git.example.com/apps/image-shrink.git",
			"machine git.example.com\nlogin image-shrink-agent\npassword " + gitToken + "\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ".netrc")
			// One the image left, readable by all, is replaced with the agent's own.
			if err := os.WriteFile(path, []byte("machine old\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			job := Job{RepoURL: tt.repoURL, Username: "image-shrink-agent", Token: gitToken}
			if err := WriteNetrc(home, job); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf(".netrc = %q, want %q", got, tt.want)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if mode := info.Mode().Perm(); mode != 0o600 {
				t.Errorf(".netrc mode %v, want 0600", mode)
			}
		})
	}

	t.Run("none without a host or credentials", func(t *testing.T) {
		home := t.TempDir()
		for _, job := range []Job{
			{RepoURL: "/srv/git/image-shrink.git", Username: "bot", Token: gitToken},
			{RepoURL: "https://git.example.com/apps/image-shrink.git"},
		} {
			if err := WriteNetrc(home, job); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := os.Stat(filepath.Join(home, ".netrc")); !os.IsNotExist(err) {
			t.Errorf(".netrc written: %v", err)
		}
	})
}
