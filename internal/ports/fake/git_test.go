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

package fake_test

import (
	"context"
	"maps"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/ports/fake"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// The fake reads a plan branch as the planner leaves it: main with its plan/
// replaced, at the branch's head.
func TestGitReadsAPlanBranch(t *testing.T) {
	g := fake.New()
	p := g.For(ports.Config{URL: "http://gitea", Owner: "apps"})
	ctx := context.Background()
	repo, _ := p.EnsureRepository(ctx, "brief-cel-lens", "")
	if _, err := p.PushInitialCommit(ctx, repo, scaffold.Tree{
		"brief.md":       {Data: []byte("idea\n")},
		"plan/plan.yaml": {Data: []byte("round 1\n")},
		"plan/old.md":    {Data: []byte("gone in round 2\n")},
	}, "Workspace", ports.Signature{}); err != nil {
		t.Fatal(err)
	}
	sha := g.PushPlan("apps", "brief-cel-lens", 2, scaffold.Tree{
		"plan/plan.yaml":         {Data: []byte("round 2\n")},
		"plan/issues/orders.yml": {Data: []byte("- title: x\n")},
	})

	files, at, err := p.ReadFiles(ctx, repo, "plan/2", []string{"plan/", "brief.md", "missing/"})
	if err != nil {
		t.Fatal(err)
	}
	want := scaffold.Tree{
		"brief.md":               {Data: []byte("idea\n")},
		"plan/plan.yaml":         {Data: []byte("round 2\n")},
		"plan/issues/orders.yml": {Data: []byte("- title: x\n")},
	}
	if at != sha || !maps.EqualFunc(files, want, func(a, b scaffold.File) bool { return string(a.Data) == string(b.Data) }) {
		t.Errorf("ReadFiles(plan/2) = %v at %s, want %v at %s", files, at, want, sha)
	}
	if main, _, _ := p.ReadFiles(ctx, repo, ports.DefaultBranch, []string{"plan/"}); len(main) != 2 {
		t.Errorf("ReadFiles(main) = %v, want main's own plan/", main)
	}
	if _, _, err := p.ReadFiles(ctx, repo, "plan/3", []string{"plan/"}); err == nil {
		t.Error("ReadFiles() of a branch the fake doesn't have succeeded")
	}
}

// A branch pushed from a seeded repository's commit updates with main as it
// is, and a pull request on a branch of a repository the fake has none for
// updates too, without a panic (#1170).
func TestGitUpdatesPullRequestsOnSeededAndMissingRepositories(t *testing.T) {
	g := fake.New()
	p := g.For(ports.Config{URL: "http://gitea", Owner: "apps"})
	ctx := context.Background()
	g.SeedRepoFiles("apps", "adopted", "Import", scaffold.Tree{"AGENTS.md": {Data: []byte("# theirs\n")}})
	repo := ports.Repository{Owner: "apps", Name: "adopted"}
	base, err := p.BranchCommit(ctx, repo, ports.DefaultBranch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.PushBranch(ctx, repo, base, "standard/v2", scaffold.Tree{"AGENTS.md": {Data: []byte("# ours\n")}},
		nil, "Bump the Standard", ports.Signature{}); err != nil {
		t.Fatal(err)
	}
	pr, err := p.OpenPullRequest(ctx, repo, "standard/v2", ports.DefaultBranch, "Bump", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.UpdatePullRequest(ctx, repo, pr.Number); err != nil {
		t.Errorf("UpdatePullRequest() of a branch on the seeded commit = %v, want no conflict", err)
	}

	missing := ports.Repository{Owner: "apps", Name: "missing"}
	g.PushBranchHead("apps", "missing", "task/a", "abc123")
	pr, err = p.OpenPullRequest(ctx, missing, "task/a", ports.DefaultBranch, "A", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.UpdatePullRequest(ctx, missing, pr.Number); err != nil {
		t.Errorf("UpdatePullRequest() in a repository the fake has none for = %v", err)
	}
}
