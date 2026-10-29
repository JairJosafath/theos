package fake_test

import (
	"context"
	"maps"
	"testing"

	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/fake"
	"github.com/JairJosafath/c4factory/internal/scaffold"
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
