package controller

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/c4factory/internal/gitprovider"
)

// The fake host's CI runners: a registration token per owner organization,
// and the runners registered with it, as a runner pod's registration makes
// them.

var _ gitprovider.OwnerRunners = (*fakeProvider)(nil)

type fakeRunnerState struct {
	// runners is each owner's runners, oldest first, and lastID the newest
	// runner's ID, unique across owners as the host's are.
	runners map[string][]gitprovider.Runner
	lastID  int64
	// tokenErr is what each owner's registration token call fails with.
	tokenErr map[string]error
}

func (g *fakeGit) runnerState() *fakeRunnerState {
	if g.runners == nil {
		g.runners = &fakeRunnerState{runners: map[string][]gitprovider.Runner{}}
	}
	return g.runners
}

// runnerTokenPrefix starts every owner's registration token, which is the
// prefix and the owner's name.
const runnerTokenPrefix = "fake-runner-registration-"

// failRunnerToken makes owner's registration token call fail with err; nil
// lets it answer again.
func (g *fakeGit) failRunnerToken(owner string, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.runnerState()
	if s.tokenErr == nil {
		s.tokenErr = map[string]error{}
	}
	s.tokenErr[owner] = err
}

// RunnerRegistrationToken is the same for an owner every time, unless
// failRunnerToken made it fail.
func (p *fakeProvider) RunnerRegistrationToken(context.Context) (string, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	if err := p.git.runnerState().tokenErr[p.cfg.Owner]; err != nil {
		return "", err
	}
	return runnerTokenPrefix + p.cfg.Owner, nil
}

func (p *fakeProvider) Runners(context.Context) ([]gitprovider.Runner, error) {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	return slices.Clone(p.git.runnerState().runners[p.cfg.Owner]), nil
}

// DeleteRunner removes the owner's runner id, and leaves another owner's.
func (p *fakeProvider) DeleteRunner(_ context.Context, id int64) error {
	p.git.mu.Lock()
	defer p.git.mu.Unlock()
	s := p.git.runnerState()
	s.runners[p.cfg.Owner] = slices.DeleteFunc(s.runners[p.cfg.Owner], func(r gitprovider.Runner) bool { return r.ID == id })
	return nil
}

// registerRunner registers an ephemeral runner name with labels to the owner
// whose registration token it holds, as `gitea-runner register --ephemeral`
// does, and returns it, offline until setRunnerOnline. A token that is no
// owner's is refused.
func (g *fakeGit) registerRunner(token, name string, labels ...string) (gitprovider.Runner, error) {
	owner, ok := strings.CutPrefix(token, runnerTokenPrefix)
	if !ok || owner == "" {
		return gitprovider.Runner{}, fmt.Errorf("runner registration token %q is no owner's", token)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.runnerState()
	s.lastID++
	r := gitprovider.Runner{ID: s.lastID, Name: name, Labels: labels, Ephemeral: true}
	s.runners[owner] = append(s.runners[owner], r)
	return r, nil
}

// setRunnerOnline makes owner's runner name online or offline, and busy or
// idle, as its daemon calling the host, taking a job or stopping does.
func (g *fakeGit) setRunnerOnline(owner, name string, online, busy bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, r := range g.runnerState().runners[owner] {
		if r.Name == name {
			g.runners.runners[owner][i].Online, g.runners.runners[owner][i].Busy = online, busy
		}
	}
}

// The fake keeps each owner's runners apart, as TestGiteaRunnersIntegration
// shows Gitea does.
func TestFakeGitKeepsOwnersRunnersApart(t *testing.T) {
	g := &fakeGit{repos: map[string]*fakeRepo{}, histories: map[string]int{}, hangs: map[string]chan struct{}{}}
	ctx := context.Background()
	owner := func(name string) gitprovider.OwnerRunners {
		p, _ := g.Provider(gitprovider.Config{URL: "http://gitea", Owner: name})
		return p.(gitprovider.OwnerRunners)
	}
	a, b := owner("apps"), owner("shop")
	tokenA, _ := a.RunnerRegistrationToken(ctx)
	tokenB, _ := b.RunnerRegistrationToken(ctx)
	if again, _ := a.RunnerRegistrationToken(ctx); again != tokenA || tokenA == tokenB {
		t.Fatalf("tokens %q, %q and %q: want one per owner, the same every time", tokenA, again, tokenB)
	}
	runnerA, err := g.registerRunner(tokenA, "go-a", "go:host")
	if err != nil {
		t.Fatal(err)
	}
	runnerB, _ := g.registerRunner(tokenB, "go-b", "go:host")
	if _, err := g.registerRunner("not-a-token", "go-c"); err == nil {
		t.Error("a runner registered with a token that is no owner's")
	}
	g.setRunnerOnline("apps", "go-a", true, true)

	runners, _ := a.Runners(ctx)
	want := []gitprovider.Runner{{ID: runnerA.ID, Name: "go-a", Labels: []string{"go:host"}, Ephemeral: true,
		Online: true, Busy: true}}
	if !reflect.DeepEqual(runners, want) {
		t.Errorf("apps' runners = %+v, want %+v", runners, want)
	}
	g.setRunnerOnline("apps", "go-a", false, false)
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
