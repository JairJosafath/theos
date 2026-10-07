package gitprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
		tree, err := client.FetchTemplate(ctx, base+"/templates/python-fastapi", "v1.3.0", nil)
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

	t.Run("gives a bot a repository and deletes it", func(t *testing.T) {
		p, err := client.Provider(Config{URL: base, Owner: "webshop", Credentials: creds})
		if err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("it-bot-%d", time.Now().UnixNano())
		t.Cleanup(func() { deleteRepo(t, base, "webshop", name, creds.Token) })
		repo, err := p.EnsureRepository(ctx, name, "integration test")
		if err != nil {
			t.Fatal(err)
		}
		bot, err := p.EnsureAgentAccess(ctx, repo, name+"-agent")
		if err != nil {
			t.Fatal(err)
		}
		readRepo := func(token string) int {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/repos/webshop/"+name, nil)
			req.Header.Set("Authorization", "token "+token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			return resp.StatusCode
		}
		if status := readRepo(bot.Token); status != http.StatusOK {
			t.Fatalf("the bot reads its repository: HTTP %d", status)
		}
		for i := range 2 { // the second finds the user gone
			if err := p.RemoveAgentAccess(ctx, repo, bot.Username); err != nil {
				t.Fatalf("RemoveAgentAccess() #%d: %v", i, err)
			}
		}
		if status := readRepo(bot.Token); status != http.StatusUnauthorized {
			t.Errorf("the deleted bot's token reads the repository: HTTP %d, want 401", status)
		}
		if status := readRepo(creds.Token); status != http.StatusOK {
			t.Errorf("the repository went with its bot: HTTP %d", status)
		}
	})
}

// TestGiteaTemplatesIntegration checks that the seeded Gitea has every template
// the catalog pins, at its tag, like TestGiteaIntegration.
func TestGiteaTemplatesIntegration(t *testing.T) {
	base := os.Getenv("C4_GITEA_URL")
	if base == "" {
		t.Skip("C4_GITEA_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := Client{}

	data, err := os.ReadFile("../../templates/versions.txt")
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(data)) {
		name, tag, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if tree, err := client.FetchTemplate(ctx, base+"/templates/"+name, tag, nil); err != nil || len(tree) == 0 {
			t.Errorf("templates/%s@%s: %d files, %v", name, tag, len(tree), err)
		}
	}
}

// TestGiteaChecksIntegration protects a branch and reads the checks on it against
// the seeded Gitea, like TestGiteaIntegration.
func TestGiteaChecksIntegration(t *testing.T) {
	base := os.Getenv("C4_GITEA_URL")
	if base == "" {
		t.Skip("C4_GITEA_URL not set")
	}
	creds := Credentials{Username: os.Getenv("C4_GITEA_USERNAME"), Token: os.Getenv("C4_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := Client{}

	p, err := client.Provider(Config{URL: base, Owner: "webshop", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("it-checks-%d", time.Now().UnixNano())
	t.Cleanup(func() { deleteRepo(t, base, "webshop", name, creds.Token) })
	repo, err := p.EnsureRepository(ctx, name, "integration test")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := p.PushInitialCommit(ctx, repo, scaffold.Tree{"Makefile": {Data: []byte("check:\n")}}, "Scaffold\n", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // the second updates the rule
		if err := p.ProtectBranch(ctx, repo, "main", BranchRule{StatusContexts: []string{"check*"}}); err != nil {
			t.Fatal(err)
		}
	}
	var rules []struct {
		RuleName          string   `json:"rule_name"`
		EnablePush        bool     `json:"enable_push"`
		EnableStatusCheck bool     `json:"enable_status_check"`
		Contexts          []string `json:"status_check_contexts"`
	}
	giteaJSON(t, base, creds.Token, http.MethodGet, "/api/v1/repos/webshop/"+name+"/branch_protections", nil, &rules)
	if len(rules) != 1 || rules[0].RuleName != "main" || rules[0].EnablePush || !rules[0].EnableStatusCheck ||
		strings.Join(rules[0].Contexts, ",") != "check*" {
		t.Errorf("branch protections = %+v, want one rule on main: no pushes, checks check*", rules)
	}

	if got, err := p.CombinedStatus(ctx, repo, "main"); err != nil || got.Commit != hash || got.State != "" {
		t.Fatalf("CombinedStatus() before any check = %+v, %v", got, err)
	}
	// A Gitea Actions run of the Standard's workflow reports like this.
	giteaJSON(t, base, creds.Token, http.MethodPost, "/api/v1/repos/webshop/"+name+"/statuses/"+hash,
		map[string]string{"state": "success", "context": "check / check (push)", "target_url": base + "/runs/1"}, nil)
	got, err := p.CombinedStatus(ctx, repo, "main")
	if err != nil || got.State != CheckSuccess || got.Contexts["check / check (push)"] != CheckSuccess {
		t.Errorf("CombinedStatus() = %+v, %v; want success", got, err)
	}
}

// TestGiteaIssuesIntegration runs the issue flow against the seeded Gitea, like
// TestGiteaIntegration.
func TestGiteaIssuesIntegration(t *testing.T) {
	base := os.Getenv("C4_GITEA_URL")
	if base == "" {
		t.Skip("C4_GITEA_URL not set")
	}
	creds := Credentials{Username: os.Getenv("C4_GITEA_USERNAME"), Token: os.Getenv("C4_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := Client{}

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
		blocker, err := p.CreateIssue(ctx, repo, "First", "body", nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		blocked, err := p.CreateIssue(ctx, repo, "Second", "body", []string{"status/ready"}, 0)
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
		if err := p.EditIssueBody(ctx, repo, blocked.Number, "body\n\n## Clarification\n\nMore."); err != nil {
			t.Fatal(err)
		}
		if got, err := p.GetIssue(ctx, repo, blocked.Number); err != nil || got.Body != "body\n\n## Clarification\n\nMore." ||
			strings.Join(got.Labels, ",") != "status/in-progress" {
			t.Fatalf("after EditIssueBody() = %+v, %v; want the new body, labels kept", got, err)
		}
		if err := p.RemoveIssueLabel(ctx, repo, blocked.Number, "status/in-progress"); err != nil {
			t.Fatal(err)
		}
	})
}

// TestGiteaCrossRepositoryDependencyIntegration makes an issue depend on an
// issue of another repository against the seeded Gitea, like
// TestGiteaIntegration: a CLI's issue on its engine's.
func TestGiteaCrossRepositoryDependencyIntegration(t *testing.T) {
	base := os.Getenv("C4_GITEA_URL")
	if base == "" {
		t.Skip("C4_GITEA_URL not set")
	}
	creds := Credentials{Username: os.Getenv("C4_GITEA_USERNAME"), Token: os.Getenv("C4_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	p, err := Client{}.Provider(Config{URL: base, Owner: "webshop", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().UnixNano()
	var repos [2]Repository
	for i, name := range []string{fmt.Sprintf("it-deps-cli-%d", stamp), fmt.Sprintf("it-deps-engine-%d", stamp)} {
		t.Cleanup(func() { deleteRepo(t, base, "webshop", name, creds.Token) })
		if repos[i], err = p.EnsureRepository(ctx, name, "integration test"); err != nil {
			t.Fatal(err)
		}
	}
	cli, engine := repos[0], repos[1]
	blocker, err := p.CreateIssue(ctx, engine, "Engine first", "body", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	blocked, err := p.CreateIssue(ctx, cli, "CLI after", "body", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("webshop/%s#%d", engine.Name, blocker.Number)
	for range 2 {
		if err := p.AddIssueDependency(ctx, cli, blocked.Number, engine, blocker.Number); err != nil {
			t.Fatalf("AddIssueDependency() = %v", err)
		}
		if open, err := p.OpenBlockers(ctx, cli, blocked.Number); err != nil || len(open) != 1 || open[0] != want {
			t.Fatalf("OpenBlockers() = %v, %v; want [%s] once", open, err, want)
		}
	}
}

// TestGiteaReleasesIntegration reads a release, streams its asset and sets its
// notes against the seeded Gitea, like TestGiteaIntegration.
func TestGiteaReleasesIntegration(t *testing.T) {
	base := os.Getenv("C4_GITEA_URL")
	if base == "" {
		t.Skip("C4_GITEA_URL not set")
	}
	creds := Credentials{Username: os.Getenv("C4_GITEA_USERNAME"), Token: os.Getenv("C4_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	p, err := Client{}.Provider(Config{URL: base, Owner: "webshop", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("it-releases-%d", time.Now().UnixNano())
	t.Cleanup(func() { deleteRepo(t, base, "webshop", name, creds.Token) })
	repo, err := p.EnsureRepository(ctx, name, "integration test")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := p.PushInitialCommit(ctx, repo, scaffold.Tree{"README.md": {Data: []byte("hi")}}, "Scaffold\n", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	if rel, err := p.GetRelease(ctx, repo, "v0.1.0"); err != nil || rel != nil {
		t.Fatalf("GetRelease() before any release = %+v, %v; want none", rel, err)
	}

	// The release run makes the release and attaches the files, as here.
	id := createRelease(t, base, creds.Token, repo, "v0.1.0", commit)
	file := strings.Repeat("orders-cli ", 100_000)
	uploadAsset(t, base, creds.Token, repo, id, "orders-cli", file)

	rel, err := p.GetRelease(ctx, repo, "v0.1.0")
	if err != nil || rel == nil {
		t.Fatalf("GetRelease() = %+v, %v", rel, err)
	}
	if rel.ID != id || rel.Tag != "v0.1.0" || rel.Body != "" || rel.Draft || len(rel.Assets) != 1 ||
		rel.Assets[0].Name != "orders-cli" || rel.Assets[0].Size != int64(len(file)) {
		t.Fatalf("GetRelease() = %+v, want release %d with orders-cli of %d bytes", rel, id, len(file))
	}

	body, size, err := p.OpenReleaseAsset(ctx, repo, rel.Assets[0])
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || size != int64(len(file)) || string(data) != file {
		t.Fatalf("OpenReleaseAsset() read %d bytes of %d, %v; want the uploaded file", len(data), size, err)
	}

	if err := p.SetReleaseBody(ctx, repo, id, "- Add orders (#3)\n"); err != nil {
		t.Fatal(err)
	}
	if rel, err := p.GetRelease(ctx, repo, "v0.1.0"); err != nil || rel == nil || rel.Body != "- Add orders (#3)\n" ||
		len(rel.Assets) != 1 {
		t.Fatalf("GetRelease() after SetReleaseBody = %+v, %v; want the notes and the file kept", rel, err)
	}
}

func TestGiteaMilestonesIntegration(t *testing.T) {
	base := os.Getenv("C4_GITEA_URL")
	if base == "" {
		t.Skip("C4_GITEA_URL not set")
	}
	creds := Credentials{Username: os.Getenv("C4_GITEA_USERNAME"), Token: os.Getenv("C4_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	p, err := Client{}.Provider(Config{URL: base, Owner: "webshop", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("it-milestones-%d", time.Now().UnixNano())
	t.Cleanup(func() { deleteRepo(t, base, "webshop", name, creds.Token) })
	repo, err := p.EnsureRepository(ctx, name, "integration test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.EnsureMilestone(ctx, repo, "Later", "Not part of any release"); err != nil {
		t.Fatal(err)
	}
	const marker = "<!-- c4-brief: cel-lens/1 -->"
	// Noon, so the day is the same in the server's time zone.
	y, m, d := time.Now().UTC().AddDate(0, 0, 7).Date()
	due := time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
	made, err := p.CreateMilestone(ctx, repo, "v0.1.0", "The first release.\n\n"+marker, due)
	if err != nil {
		t.Fatal(err)
	}
	// Closing keeps the due date's day, due at its end.
	sameDay := func(a, b time.Time) bool {
		ay, am, ad := a.UTC().Date()
		by, bm, bd := b.UTC().Date()
		return ay == by && am == bm && ad == bd
	}
	if made.Title != "v0.1.0" || made.Closed || !sameDay(made.Due, due) {
		t.Fatalf("CreateMilestone() = %+v, want v0.1.0, open, due %v", made, due)
	}

	find := func() Milestone {
		t.Helper()
		all, err := p.ListAllMilestones(ctx, repo)
		if err != nil {
			t.Fatal(err)
		}
		var found []Milestone
		for _, m := range all {
			if strings.HasSuffix(m.Description, marker) {
				found = append(found, m)
			}
		}
		if len(all) != 2 || len(found) != 1 || found[0].ID != made.ID || found[0].Title != "v0.1.0" ||
			!sameDay(found[0].Due, due) {
			t.Fatalf("ListAllMilestones() = %+v, want Later and v0.1.0 found by its description", all)
		}
		return found[0]
	}
	if m := find(); m.Closed {
		t.Errorf("milestone %+v closed before CloseMilestone", m)
	}
	if err := p.CloseMilestone(ctx, repo, made.ID); err != nil {
		t.Fatal(err)
	}
	if m := find(); !m.Closed {
		t.Errorf("milestone %+v open after CloseMilestone", m)
	}
	if open, err := p.ListMilestones(ctx, repo); err != nil || len(open) != 1 || open[0].Title != "Later" {
		t.Errorf("ListMilestones() after closing = %+v, %v; want Later only", open, err)
	}
}

// createRelease releases tag at commit, as the release run does, and returns
// the release's ID. It waits for Gitea to see the push: its "empty" flag lags
// behind one.
func createRelease(t *testing.T, base, token string, repo Repository, tag, commit string) int64 {
	t.Helper()
	body := fmt.Sprintf(`{"tag_name":%q,"target_commitish":%q,"name":%q}`, tag, commit, tag)
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/repos/%s/%s/releases", base, repo.Owner,
			repo.Name), strings.NewReader(body))
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		var rel struct {
			ID int64 `json:"id"`
		}
		switch {
		case resp.StatusCode == http.StatusCreated && json.Unmarshal(data, &rel) == nil:
			return rel.ID
		case resp.StatusCode == http.StatusUnprocessableEntity && apiMessage(data) == "repo is empty" &&
			time.Now().Before(deadline):
		default:
			t.Fatalf("creating release %s: HTTP %d: %s", tag, resp.StatusCode, apiMessage(data))
		}
	}
}

// uploadAsset attaches a file called name to release id.
func uploadAsset(t *testing.T, base, token string, repo Repository, id int64, name, content string) {
	t.Helper()
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	part, _ := form.CreateFormFile("attachment", name)
	_, _ = part.Write([]byte(content))
	_ = form.Close()
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/api/v1/repos/%s/%s/releases/%d/assets?name=%s",
		base, repo.Owner, repo.Name, id, url.QueryEscape(name)), &buf)
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("uploading %s: HTTP %d", name, resp.StatusCode)
	}
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

// giteaJSON sends body, if any, as JSON to a Gitea API path and reads the reply
// into into, if any.
func giteaJSON(t *testing.T, base, token, method, path string, body, into any) {
	t.Helper()
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		payload = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, strings.TrimSuffix(base, "/")+path, payload)
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, data)
	}
	if into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatal(err)
		}
	}
}

//nolint:unparam // takes owner and name like the API
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

// TestGiteaGoModuleIntegration publishes a tagged repository's Go module to the
// seeded Gitea's registry and fetches it with the go command through that
// registry alone, like TestGiteaIntegration.
func TestGiteaGoModuleIntegration(t *testing.T) {
	base := os.Getenv("C4_GITEA_URL")
	if base == "" {
		t.Skip("C4_GITEA_URL not set")
	}
	creds := Credentials{Username: os.Getenv("C4_GITEA_USERNAME"), Token: os.Getenv("C4_GITEA_TOKEN")}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	p, err := Client{}.Provider(Config{URL: base, Owner: "webshop", Credentials: creds})
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("it-gomod-%d", time.Now().UnixNano())
	t.Cleanup(func() { deleteRepo(t, base, "webshop", name, creds.Token) })
	repo, err := p.EnsureRepository(ctx, name, "integration test")
	if err != nil {
		t.Fatal(err)
	}
	modPath := "c4factory.local/webshop/" + name
	commit, err := p.PushInitialCommit(ctx, repo, scaffold.Tree{
		"go.mod":           {Data: []byte("module " + modPath + "\n\ngo 1.24\n")},
		"greet/greet.go":   {Data: []byte("package greet\n\nfunc Hello() string { return \"hello\" }\n")},
		"tools/go.mod":     {Data: []byte("module " + modPath + "/tools\n\ngo 1.24\n")},
		"tools/gen/gen.go": {Data: []byte("package gen\n")},
	}, "Scaffold\n", testAuthor)
	if err != nil {
		t.Fatal(err)
	}
	// The tag waits for Gitea to see the push, as a release does.
	createRelease(t, base, creds.Token, repo, "v0.1.0", commit)

	got, err := p.PublishGoModule(ctx, repo, "v0.1.0")
	if want := (GoModule{Outcome: GoModulePublished, Path: modPath, Version: "v0.1.0"}); err != nil || got != want {
		t.Fatalf("PublishGoModule() = %+v, %v; want %+v", got, err, want)
	}

	// The go command fetches it from the registry alone, signed in with a netrc
	// as an agent pod is.
	home := t.TempDir()
	u, _ := url.Parse(base)
	netrc := filepath.Join(home, ".netrc")
	if err := os.WriteFile(netrc, []byte(fmt.Sprintf("machine %s login %s password %s\n", u.Hostname(),
		creds.Username, creds.Token)), 0o600); err != nil {
		t.Fatal(err)
	}
	modCache := filepath.Join(home, "mod")
	cmd := exec.CommandContext(ctx, "go", "mod", "download", "-json", modPath+"@v0.1.0")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "GOPROXY="+strings.TrimSuffix(base, "/")+"/api/packages/webshop/go",
		"GONOSUMDB=c4factory.local", "GOFLAGS=-modcacherw", "GOMODCACHE="+modCache, "NETRC="+netrc,
		"GOWORK=off", "GO111MODULE=on", "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go mod download: %v\n%s", err, out)
	}
	var downloaded struct{ Dir string }
	if err := json.Unmarshal(out, &downloaded); err != nil || downloaded.Dir == "" {
		t.Fatalf("go mod download printed %s", out)
	}
	if _, err := os.Stat(filepath.Join(downloaded.Dir, "greet", "greet.go")); err != nil {
		t.Errorf("the module misses greet/greet.go: %v", err)
	}
	if _, err := os.Stat(filepath.Join(downloaded.Dir, "tools")); !os.IsNotExist(err) {
		t.Errorf("the module has the nested module tools/: %v", err)
	}

	if got, err := p.PublishGoModule(ctx, repo, "v0.1.0"); err != nil || got.Outcome != GoModuleExists {
		t.Fatalf("PublishGoModule() again = %+v, %v; want %s", got, err, GoModuleExists)
	}
}
