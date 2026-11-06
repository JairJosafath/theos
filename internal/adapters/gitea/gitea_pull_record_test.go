/*
Copyright 2026 JairJosafath. All rights reserved.

This file is part of theos, which is proprietary. No license is granted
to use, copy, modify or distribute it without the copyright holder's written
permission. See the LICENSE file.
*/

package gitea

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
)

// A merged pull request's size and its commits, for the factory's metrics
// (docs/plans/done/measure-and-improve.md, decisions 3 and 4).
func TestPullRequestSizeAndCommits(t *testing.T) {
	const repo = "/api/v1/repos/apps/photo-editor"
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(v any) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(v)
		}
		switch r.Method + " " + r.URL.Path {
		case "GET " + repo + "/pulls/9":
			reply(map[string]any{"number": 9, "state": "closed", "merged": true, "additions": 120, "deletions": 30,
				"changed_files": 4, "head": map[string]any{"ref": "task/add-crop", "sha": "abc"}})
		case "GET " + repo + "/pulls/9/commits":
			queries = append(queries, r.URL.RawQuery)
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			n := issuePageSize
			if page == 2 {
				n = 2
			}
			commits := make([]map[string]any, 0, n)
			for i := range n {
				parents := []map[string]string{{"sha": "p1"}}
				email := "apps-photo-editor-agent@agents.theos.factory.io"
				if page == 2 && i == 0 {
					parents = append(parents, map[string]string{"sha": "p2"})
					email = "c4admin@example.com"
				}
				commits = append(commits, map[string]any{"sha": fmt.Sprintf("c%d-%d", page, i),
					"commit": map[string]any{"author": map[string]string{"name": "x", "email": email}}, "parents": parents})
			}
			reply(commits)
		case "GET " + repo + "/pulls/8/commits":
			w.WriteHeader(http.StatusNotFound)
			reply(map[string]string{"message": "pull request does not exist"})
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	p, err := Client{}.Provider(ports.Config{URL: srv.URL, Owner: "apps",
		Credentials: ports.Credentials{Username: "c4admin", Token: "token"}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	photo := p.Repository("photo-editor")

	pr, err := p.GetPullRequest(ctx, photo, 9)
	if err != nil || pr.Additions != 120 || pr.Deletions != 30 || pr.ChangedFiles != 4 {
		t.Fatalf("GetPullRequest() = %+v, %v; want 120 added, 30 removed, 4 files", pr, err)
	}
	commits, err := p.PullRequestCommits(ctx, photo, 9)
	if err != nil || len(commits) != issuePageSize+2 {
		t.Fatalf("PullRequestCommits() = %d commits, %v; want both pages", len(commits), err)
	}
	merge := commits[issuePageSize]
	if merge != (ports.PullCommit{SHA: "c2-0", AuthorEmail: "c4admin@example.com", Parents: 2}) ||
		commits[0] != (ports.PullCommit{SHA: "c1-0", AuthorEmail: "apps-photo-editor-agent@agents.theos.factory.io",
			Parents: 1}) {
		t.Errorf("commits read as %+v and %+v", commits[0], merge)
	}
	want := []string{"page=1&limit=50&verification=false&files=false", "page=2&limit=50&verification=false&files=false"}
	if fmt.Sprint(queries) != fmt.Sprint(want) {
		t.Errorf("queries = %v, want %v", queries, want)
	}
	if _, err := p.PullRequestCommits(ctx, photo, 8); !errors.Is(err, ports.ErrPullRequestNotFound) {
		t.Errorf("PullRequestCommits() of a pull request that doesn't exist: %v, want ErrPullRequestNotFound", err)
	}
}
