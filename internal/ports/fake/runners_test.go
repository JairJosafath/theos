package fake_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/JairJosafath/c4factory/internal/ports"
	"github.com/JairJosafath/c4factory/internal/ports/fake"
)

// The fake keeps each owner's runners apart, as TestGiteaRunnersIntegration
// shows Gitea does.
func TestGitKeepsOwnersRunnersApart(t *testing.T) {
	g := fake.New()
	ctx := context.Background()
	owner := func(name string) ports.OwnerRunners {
		return g.For(ports.Config{URL: "http://gitea", Owner: name})
	}
	a, b := owner("apps"), owner("shop")
	tokenA, _ := a.RunnerRegistrationToken(ctx)
	tokenB, _ := b.RunnerRegistrationToken(ctx)
	if again, _ := a.RunnerRegistrationToken(ctx); again != tokenA || tokenA == tokenB {
		t.Fatalf("tokens %q, %q and %q: want one per owner, the same every time", tokenA, again, tokenB)
	}
	runnerA, err := g.RegisterRunner(tokenA, "go-a", "go:host")
	if err != nil {
		t.Fatal(err)
	}
	runnerB, _ := g.RegisterRunner(tokenB, "go-b", "go:host")
	if _, err := g.RegisterRunner("not-a-token", "go-c"); err == nil {
		t.Error("a runner registered with a token that is no owner's")
	}
	g.SetRunnerOnline("apps", "go-a", true, true)

	runners, _ := a.Runners(ctx)
	want := []ports.Runner{{ID: runnerA.ID, Name: "go-a", Labels: []string{"go:host"}, Ephemeral: true,
		Online: true, Busy: true}}
	if !reflect.DeepEqual(runners, want) {
		t.Errorf("apps' runners = %+v, want %+v", runners, want)
	}
	g.SetRunnerOnline("apps", "go-a", false, false)
	if runners, _ := a.Runners(ctx); len(runners) != 1 || runners[0].Online || runners[0].Busy {
		t.Errorf("apps' runners after go-a stopped = %+v, want it offline", runners)
	}
	if runnerA.ID == runnerB.ID {
		t.Error("two owners' runners have one ID")
	}

	_ = a.DeleteRunner(ctx, runnerB.ID)
	if runners, _ := b.Runners(ctx); len(runners) != 1 || runners[0].Online {
		t.Errorf("shop's runners = %+v, want go-b, offline, kept from apps' delete", runners)
	}
	_ = a.DeleteRunner(ctx, runnerA.ID)
	if runners, _ := a.Runners(ctx); len(runners) != 0 {
		t.Errorf("apps' runners after its delete = %+v, want none", runners)
	}
}
