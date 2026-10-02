package gitprovider

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/c4factory/internal/scaffold"
)

// TestGiteaIntegration runs against the seeded Gitea of the test environment.
// It is skipped unless C4_GITEA_URL is set; `make test-gitea` sets it up:
//
//	C4_GITEA_URL=http://localhost:3000 C4_GITEA_TOKEN=... go test ./internal/gitprovider/ -run Integration
func TestGiteaIntegration(t *testing.T) {
	base := os.Getenv("C4_GITEA_URL")
	if base == "" {
		t.Skip("C4_GITEA_URL not set")
	}
	creds := Credentials{Username: os.Getenv("C4_GITEA_USERNAME"), Token: os.Getenv("C4_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := Client{}

	t.Run("fetches a seeded template at its tag", func(t *testing.T) {
		tree, err := client.FetchTemplate(ctx, base+"/templates/python-fastapi", "v1.2.1", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{"pyproject.toml.tmpl", "app/main.py.tmpl", ".c4factory/devcontainer.json"} {
			if _, ok := tree[p]; !ok {
				t.Errorf("template misses %s", p)
			}
		}
		if _, err := client.FetchTemplate(ctx, base+"/templates/python-fastapi", "main", nil); err == nil {
			t.Error("a branch ref was accepted")
		}
	})

	t.Run("creates, fills and reads a repository", func(t *testing.T) {
		p, err := client.Provider(Config{URL: base, Owner: "webshop", Credentials: creds})
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("it-%d", time.Now().UnixNano())
		t.Cleanup(func() { deleteRepo(t, base, "webshop", name, creds.Token) })

		repo, err := p.EnsureRepository(ctx, name, "integration test")
		if err != nil {
			t.Fatal(err)
		}
		if again, err := p.EnsureRepository(ctx, name, "integration test"); err != nil || again != repo {
			t.Fatalf("EnsureRepository is not idempotent: %+v, %v", again, err)
		}
		if commits, err := p.History(ctx, repo, 2); err != nil || len(commits) != 0 {
			t.Fatalf("History(new) = %v, %v", commits, err)
		}
		hash, err := p.PushInitialCommit(ctx, repo, scaffold.Tree{"README.md": {Data: []byte("hi")}},
			"Scaffold\n\nC4-Factory-Container: webshop/"+name+"\n", testAuthor)
		if err != nil {
			t.Fatal(err)
		}
		commits, err := p.History(ctx, repo, 2)
		if err != nil || len(commits) != 1 || commits[0].Hash != hash {
			t.Fatalf("History() = %v, %v; want the initial commit", commits, err)
		}
	})

	t.Run("labels, links and closes issues", func(t *testing.T) {
		p, err := client.Provider(Config{URL: base, Owner: "webshop", Credentials: creds})
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("it-issues-%d", time.Now().UnixNano())
		t.Cleanup(func() { deleteRepo(t, base, "webshop", name, creds.Token) })
		repo, err := p.EnsureRepository(ctx, name, "integration test")
		if err != nil {
			t.Fatal(err)
		}
		labels := []Label{{Name: "status/ready", Color: "#0e8a16", Exclusive: true},
			{Name: "status/in-progress", Color: "#fbca04", Exclusive: true}}
		if err := p.EnsureLabels(ctx, repo, labels); err != nil {
			t.Fatal(err)
		}
		if err := p.EnsureLabels(ctx, repo, labels); err != nil {
			t.Fatalf("EnsureLabels() again: %v", err)
		}
		blocker, err := p.CreateIssue(ctx, repo, "First", "body", nil)
		if err != nil {
			t.Fatal(err)
		}
		blocked, err := p.CreateIssue(ctx, repo, "Second", "body", []string{"status/ready"})
		if err != nil {
			t.Fatal(err)
		}
		addDependency(t, base, creds.Token, repo, blocked.Number, blocker.Number)
		if open, err := p.OpenBlockers(ctx, repo, blocked.Number); err != nil || len(open) != 1 ||
			open[0] != fmt.Sprintf("#%d", blocker.Number) {
			t.Fatalf("OpenBlockers() = %v, %v", open, err)
		}
		if err := p.CloseIssue(ctx, repo, blocker.Number); err != nil {
			t.Fatal(err)
		}
		if open, err := p.OpenBlockers(ctx, repo, blocked.Number); err != nil || len(open) != 0 {
			t.Fatalf("OpenBlockers() after closing = %v, %v", open, err)
		}
		if ready, err := p.ListIssues(ctx, repo, "status/ready"); err != nil || len(ready) != 1 {
			t.Fatalf("ListIssues() = %+v, %v", ready, err)
		}
		if err := p.AddIssueLabel(ctx, repo, blocked.Number, "status/in-progress"); err != nil {
			t.Fatal(err)
		}
		if got, err := p.GetIssue(ctx, repo, blocked.Number); err != nil || strings.Join(got.Labels, ",") != "status/in-progress" {
			t.Fatalf("labels = %v, %v; want the exclusive label to replace status/ready", got.Labels, err)
		}
		if err := p.CommentIssue(ctx, repo, blocked.Number, "Picked up"); err != nil {
			t.Fatal(err)
		}
		if err := p.RemoveIssueLabel(ctx, repo, blocked.Number, "status/in-progress"); err != nil {
			t.Fatal(err)
		}
	})
}

// addDependency makes issue blocked depend on blocker, as a person would in Gitea.
func addDependency(t *testing.T, base, token string, repo Repository, blocked, blocker int) {
	t.Helper()
	body := fmt.Sprintf(`{"owner":%q,"repo":%q,"index":%d}`, repo.Owner, repo.Name, blocker)
	req, _ := http.NewRequest(http.MethodPost,
		fmt.Sprintf("%s/api/v1/repos/%s/%s/issues/%d/dependencies", base, repo.Owner, repo.Name, blocked), strings.NewReader(body))
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("adding a dependency: HTTP %d", resp.StatusCode)
	}
}

func deleteRepo(t *testing.T, base, owner, name, token string) {
	req, _ := http.NewRequest(http.MethodDelete, strings.TrimSuffix(base, "/")+"/api/v1/repos/"+owner+"/"+name, nil)
	req.Header.Set("Authorization", "token "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Logf("cleanup of %s/%s failed: %v", owner, name, err)
		return
	}
	_ = resp.Body.Close()
}
