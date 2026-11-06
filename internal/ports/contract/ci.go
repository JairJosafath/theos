package contract

import (
	"context"
	"errors"
	"testing"

	"github.com/JairJosafath/theos/internal/ports"
)

// CIEnv builds the CIs under test and does on their host what a runner does
// there.
type CIEnv struct {
	// New returns the GitHost of a fresh owner that holds no repository,
	// where the suite makes the repositories, and the owner's CI.
	New func(t *testing.T) (ports.GitHost, ports.CI)
	// NoLog returns the check URL of a CI job of repo, whose main holds a
	// commit, that started and sent no log line, as one whose runner went
	// away before its first does. It may push to repo through host.
	NoLog func(t *testing.T, host ports.GitHost, repo ports.Repository) string
	// Cannot names the subtests the host can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// CI states what the controllers rely on of every CI that needs no runner to
// see: a repository with no workflow has no runs of a commit or a tag;
// RunnersOnline answers, or says it doesn't know; and the log of a job that
// sent none is ErrNoCILog.
func CI(t *testing.T, env CIEnv) {
	t.Helper()
	ctx, cancel := suiteContext()
	defer cancel()
	runSubtests(t, ciSuite{ctx: ctx, env: env}, env.Cannot, []subtest[ciSuite]{
		{"has no runs of a repository with no workflow", ciSuite.noRuns},
		{"says how many runners are online, or that it doesn't know", ciSuite.runnersOnline},
		{"has no log of a job that sent none", ciSuite.noLog},
	})
}

// ciSuite holds what every subtest of CI shares.
type ciSuite struct {
	ctx context.Context
	env CIEnv
}

// seeded is a fresh owner's GitHost and CI, with the contract repository
// holding the initial commit, and that commit.
func (s ciSuite) seeded(t *testing.T) (ports.GitHost, ports.CI, ports.Repository, string) {
	t.Helper()
	h, ci := s.env.New(t)
	repo := must(h.EnsureRepository(s.ctx, contractRepo, "the CI contract suite"))(t)
	head := must(h.PushInitialCommit(s.ctx, repo, seedFiles, "Scaffold\n", contractAuthor))(t)
	return h, ci, repo, head
}

func (s ciSuite) noRuns(t *testing.T) {
	h, ci, repo, head := s.seeded(t)
	if err := h.CreateTag(s.ctx, repo, firstTag, head, "Release v0.1.0"); err != nil {
		t.Fatal(err)
	}
	if runs := must(ci.CommitRuns(s.ctx, repo, head))(t); len(runs) != 0 {
		t.Errorf("CommitRuns() of a repository with no workflow = %+v, want none", runs)
	}
	if runs := must(ci.TagRuns(s.ctx, repo, firstTag, head))(t); len(runs) != 0 {
		t.Errorf("TagRuns() of a repository with no workflow = %+v, want none", runs)
	}
}

func (s ciSuite) runnersOnline(t *testing.T) {
	_, ci := s.env.New(t)
	online, known := ci.RunnersOnline(s.ctx)
	if online < 0 || !known && online != 0 {
		t.Errorf("RunnersOnline() = %d, %t; want a count, or 0 when it doesn't know", online, known)
	}
}

func (s ciSuite) noLog(t *testing.T) {
	h, ci, repo, _ := s.seeded(t)
	checkURL := s.env.NoLog(t, h, repo)
	if log, err := ci.CheckLog(s.ctx, repo, checkURL); !errors.Is(err, ports.ErrNoCILog) {
		t.Errorf("CheckLog() of a job that sent no log = %q, %v; want ErrNoCILog", log, err)
	}
}
