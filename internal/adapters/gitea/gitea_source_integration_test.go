package gitea

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
)

// TestGiteaSourceIntegration reads a tagged repository holding an executable
// and a symlink from Gitea's archive and gets each file's path, mode and
// content, and fails on a submodule. The repository is pushed with the git command, as the factory's own
// writes can't make a symlink. Like TestGiteaIntegration.
func TestGiteaSourceIntegration(t *testing.T) {
	base := os.Getenv("THEOS_GITEA_URL")
	if base == "" {
		t.Skip("THEOS_GITEA_URL not set")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git command")
	}
	creds := ports.Credentials{Username: os.Getenv("THEOS_GITEA_USERNAME"), Token: os.Getenv("THEOS_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p, err := Client{}.Provider(ports.Config{URL: base, Owner: "webshop", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("it-source-%d", time.Now().UnixNano())
	t.Cleanup(func() { deleteRepo(t, base, "webshop", name, creds.Token) })
	repo, err := p.EnsureRepository(ctx, name, "integration test")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	write := func(path, content string, mode os.FileMode) {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "# Engine\n", 0o644)
	write("bin/build.sh", "#!/bin/sh\necho build\n", 0o755)
	write("engine/go.mod", "module example.com/engine\n", 0o644)
	if err := os.Symlink("../README.md", filepath.Join(dir, "engine", "README.md")); err != nil {
		t.Fatal(err)
	}
	push, err := url.Parse(repo.CloneURL)
	if err != nil {
		t.Fatal(err)
	}
	push.User = url.UserPassword(creds.Username, creds.Token)
	gitIn(ctx, t, dir, "init", "-q", "-b", "main")
	gitIn(ctx, t, dir, "add", "-A")
	gitIn(ctx, t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "Scaffold")
	gitIn(ctx, t, dir, "push", "-q", push.String(), "main")
	commit := strings.TrimSpace(gitIn(ctx, t, dir, "rev-parse", "HEAD"))
	createRelease(t, base, creds.Token, repo, "v0.1.0", commit)

	got := map[string]readFile{}
	err = p.ReadSource(ctx, repo, "v0.1.0", func(f ports.SourceFile, r io.Reader) error {
		data, err := io.ReadAll(r)
		got[f.Path] = readFile{f, string(data)}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]readFile{
		"README.md":        {ports.SourceFile{Path: "README.md", Mode: ports.SourcePlain, Size: 9}, "# Engine\n"},
		"bin/build.sh":     {ports.SourceFile{Path: "bin/build.sh", Mode: ports.SourceExecutable, Size: 21}, "#!/bin/sh\necho build\n"},
		"engine/go.mod":    {ports.SourceFile{Path: "engine/go.mod", Mode: ports.SourcePlain, Size: 26}, "module example.com/engine\n"},
		"engine/README.md": {ports.SourceFile{Path: "engine/README.md", Mode: ports.SourceSymlink, Size: 12}, "../README.md"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ReadSource() read\n%+v\nwant\n%+v", got, want)
	}

	// A submodule is an empty folder in the archive, and fails the read.
	gitIn(ctx, t, dir, "update-index", "--add", "--cacheinfo", "160000,"+commit+",vendor/lib")
	gitIn(ctx, t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "-q", "-m", "Submodule")
	gitIn(ctx, t, dir, "push", "-q", push.String(), "main")
	createRelease(t, base, creds.Token, repo, "v0.2.0", strings.TrimSpace(gitIn(ctx, t, dir, "rev-parse", "HEAD")))
	err = p.ReadSource(ctx, repo, "v0.2.0", func(ports.SourceFile, io.Reader) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "vendor/lib is a submodule") {
		t.Errorf("ReadSource() of a submodule = %v, want vendor/lib named a submodule", err)
	}
}

// gitIn runs the git command in dir, with no user configuration, and returns
// its output.
func gitIn(ctx context.Context, t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}
