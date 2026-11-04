package agentrunner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteNpmrc(t *testing.T) {
	const repoURL = "http://gitea.gitea.svc.cluster.local:3000/apps/image-shrink.git"
	for _, tt := range []struct {
		name string
		job  Job
		want string
	}{
		{"the default scope on the Git host's registry, with the agent's Git token",
			Job{RepoURL: repoURL, Token: gitToken,
				NpmRegistry: "http://gitea.gitea.svc.cluster.local:3000/api/packages/apps/npm/"},
			"@theos:registry=http://gitea.gitea.svc.cluster.local:3000/api/packages/apps/npm/\n" +
				"//gitea.gitea.svc.cluster.local:3000/api/packages/apps/npm/:_authToken=" + gitToken + "\n"},
		{"an own scope on an external registry, with its own token",
			Job{RepoURL: "https://git.example.com/apps/image-shrink.git", Token: gitToken,
				NpmScope: "@acme", NpmRegistry: "https://npm.example.com/registry", NpmToken: "npm-read"},
			"@acme:registry=https://npm.example.com/registry\n" +
				"//npm.example.com/registry/:_authToken=npm-read\n"},
		{"an external registry without a token sends none",
			Job{RepoURL: repoURL, Token: gitToken, NpmRegistry: "https://registry.npmjs.org/"},
			"@theos:registry=https://registry.npmjs.org/\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ".npmrc")
			// One the image left, readable by all, is replaced with the agent's own.
			if err := os.WriteFile(path, []byte("registry=old\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := WriteNpmrc(home, tt.job); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf(".npmrc = %q, want %q", got, tt.want)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if mode := info.Mode().Perm(); mode != 0o600 {
				t.Errorf(".npmrc mode %v, want 0600", mode)
			}
		})
	}

	t.Run("none without a registry", func(t *testing.T) {
		home := t.TempDir()
		if err := WriteNpmrc(home, Job{RepoURL: repoURL, Token: gitToken, NpmScope: "@acme", NpmToken: "npm-read"}); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(home, ".npmrc")); !os.IsNotExist(err) {
			t.Errorf(".npmrc written: %v", err)
		}
	})

	t.Run("a registry without a host is refused", func(t *testing.T) {
		if err := WriteNpmrc(t.TempDir(), Job{RepoURL: repoURL, NpmRegistry: "/api/packages/apps/npm/"}); err == nil {
			t.Error("no error for a registry without a host")
		}
	})
}
