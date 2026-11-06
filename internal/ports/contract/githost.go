package contract

import (
	"context"
	"errors"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// GitHostEnv builds the GitHosts under test and does on their host what
// someone other than the operator does there.
type GitHostEnv struct {
	// New returns a GitHost of a fresh owner that holds no repository.
	New func(t *testing.T) ports.GitHost
	// SetStatus reports state for check context on commit, as a CI run does.
	SetStatus func(t *testing.T, repo ports.Repository, commit, context, state string)
	// Release makes the release of tag at commit, with body and one asset
	// holding content, as the release run does.
	Release func(t *testing.T, repo ports.Repository, tag, commit, body, asset, content string)
	// Cannot names the subtests the host can't run, with why; they are
	// skipped.
	Cannot map[string]string
}

// waitFor bounds how long the suite polls a host that does work in the
// background, such as testing a pull request against its base.
const waitFor = time.Minute

// contractRepo is the repository each subtest makes.
const contractRepo = "contract"

// The paths the suite's commits touch.
const (
	readme     = "README.md"
	docsA      = "docs/a.md"
	docsB      = "docs/sub/b.md"
	keepTxt    = "other/keep.txt"
	docsDir    = "docs/"
	newTxt     = "new.txt"
	otherTxt   = "other.txt"
	aTxt       = "a.txt"
	runSh      = "run.sh"
	missingTxt = "missing.txt"
)

var (
	// seedFiles are what the initial commit pushes.
	seedFiles = scaffold.Tree{
		readme:  {Data: []byte("hello\n")},
		docsA:   {Data: []byte("a\n")},
		docsB:   {Data: []byte("b\n")},
		runSh:   {Data: []byte("#!/bin/sh\necho run\n"), Executable: true},
		keepTxt: {Data: []byte("keep\n")},
	}
	contractAuthor = ports.Signature{Name: "Contract", Email: "contract@example.com",
		When: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
)

// GitHost states what the controllers rely on of every GitHost: a repository
// made once and empty; the initial push, refused on a branch that exists;
// ReadFiles of files, directories and missing paths; PushBranch and
// PushDefaultBranch, a moved base refused and ErrEmptyChange; branches and
// tags; branch rules; pull requests, opened once, ErrPullRequestNotFound, a
// merge on a stale head refused, a merge past the rule forced,
// UpdatePullRequest and ErrConflict, and their files a rename's old path
// included; combined statuses; releases; and ReadSource.
func GitHost(t *testing.T, env GitHostEnv) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	s := suite{ctx: ctx, env: env}
	for _, test := range []struct {
		name string
		run  func(suite, *testing.T)
	}{
		{"makes a repository once, empty", suite.makesRepository},
		{"pushes the initial commit once", suite.pushesInitialCommit},
		{"reads files, directories and missing paths", suite.readsFiles},
		{"pushes a branch", suite.pushesBranch},
		{"pushes the default branch", suite.pushesDefaultBranch},
		{"creates and deletes branches", suite.createsBranches},
		{"creates tags once", suite.createsTags},
		{"protects branches and tags again", suite.protects},
		{"opens and merges a pull request", suite.mergesPullRequest},
		{"refuses a merge past the rule and forces it", suite.forcesMerge},
		{"updates a pull request, or reports a conflict", suite.updatesPullRequest},
		{"lists a pull request's files, a rename's old path too", suite.listsPullRequestFiles},
		{"combines the statuses of a commit", suite.combinesStatuses},
		{"reads a release and its asset", suite.readsRelease},
		{"reads the source at a tag", suite.readsSource},
	} {
		t.Run(test.name, func(t *testing.T) {
			if why, ok := env.Cannot[test.name]; ok {
				t.Skip(why)
			}
			test.run(s, t)
		})
	}
}

// suite holds what every subtest of GitHost shares.
type suite struct {
	ctx context.Context
	env GitHostEnv
}

// seeded is a fresh host with the contract repository holding the initial
// commit, and that commit.
func (s suite) seeded(t *testing.T) (ports.GitHost, ports.Repository, string) {
	t.Helper()
	h := s.env.New(t)
	repo := must(h.EnsureRepository(s.ctx, contractRepo, "the GitHost contract suite"))(t)
	head := must(h.PushInitialCommit(s.ctx, repo, seedFiles, "Scaffold\n", contractAuthor))(t)
	return h, repo, head
}

func (s suite) makesRepository(t *testing.T) {
	h := s.env.New(t)
	repo := must(h.EnsureRepository(s.ctx, contractRepo, "the GitHost contract suite"))(t)
	again := must(h.EnsureRepository(s.ctx, contractRepo, "the GitHost contract suite"))(t)
	if again != repo || h.Repository(contractRepo) != repo {
		t.Errorf("EnsureRepository() = %+v, then %+v, Repository() %+v; want them equal", repo, again,
			h.Repository(contractRepo))
	}
	if history := must(h.History(s.ctx, repo, 10))(t); len(history) != 0 {
		t.Errorf("History() of an empty repository = %+v, want none", history)
	}
	if _, _, err := h.ReadFiles(s.ctx, repo, ports.DefaultBranch, []string{readme}); !errors.Is(err,
		ports.ErrBranchNotFound) {
		t.Errorf("ReadFiles() of an empty repository = %v, want ErrBranchNotFound", err)
	}
	if has := must(h.HasBranch(s.ctx, repo, ports.DefaultBranch))(t); has {
		t.Error("HasBranch(main) of an empty repository = true")
	}
	if commit := must(h.BranchCommit(s.ctx, repo, ports.DefaultBranch))(t); commit != "" {
		t.Errorf("BranchCommit(main) of an empty repository = %q, want none", commit)
	}
}

func (s suite) pushesInitialCommit(t *testing.T) {
	h, repo, head := s.seeded(t)
	history := must(h.History(s.ctx, repo, 10))(t)
	if len(history) != 1 || history[0].Hash != head || strings.TrimSpace(history[0].Message) != "Scaffold" {
		t.Errorf("History() = %+v, want the one commit %s", history, head)
	}
	if has := must(h.HasBranch(s.ctx, repo, ports.DefaultBranch))(t); !has {
		t.Error("HasBranch(main) after the initial push = false")
	}
	if commit := must(h.BranchCommit(s.ctx, repo, ports.DefaultBranch))(t); commit != head {
		t.Errorf("BranchCommit(main) = %q, want %s", commit, head)
	}
	if _, err := h.PushInitialCommit(s.ctx, repo, scaffold.Tree{"x.txt": {Data: []byte("x")}}, "Again",
		contractAuthor); err == nil {
		t.Error("a second initial push went through")
	}
	if commit := must(h.BranchCommit(s.ctx, repo, ports.DefaultBranch))(t); commit != head {
		t.Errorf("BranchCommit(main) after a refused push = %q, want %s", commit, head)
	}
}

func (s suite) readsFiles(t *testing.T) {
	h, repo, head := s.seeded(t)
	files, commit, err := h.ReadFiles(s.ctx, repo, ports.DefaultBranch,
		[]string{readme, docsDir, runSh, missingTxt, "nowhere/"})
	if err != nil {
		t.Fatal(err)
	}
	if commit != head {
		t.Errorf("ReadFiles() read at %s, want %s", commit, head)
	}
	wantTree(t, files, pick(seedFiles, readme, docsA, docsB, runSh))
	if _, _, err := h.ReadFiles(s.ctx, repo, "no-such-branch", []string{readme}); !errors.Is(err,
		ports.ErrBranchNotFound) {
		t.Errorf("ReadFiles() of a missing branch = %v, want ErrBranchNotFound", err)
	}
}

func (s suite) pushesBranch(t *testing.T) {
	h, repo, head := s.seeded(t)
	sha := must(h.PushBranch(s.ctx, repo, head, "task/a", scaffold.Tree{newTxt: {Data: []byte("new\n")}},
		[]string{readme, missingTxt}, "Add new.txt", contractAuthor))(t)
	files, commit := must2(h.ReadFiles(s.ctx, repo, "task/a", []string{readme, newTxt, docsA}))(t)
	if commit != sha {
		t.Errorf("ReadFiles(task/a) read at %s, want %s", commit, sha)
	}
	wantTree(t, files, scaffold.Tree{newTxt: {Data: []byte("new\n")}, docsA: seedFiles[docsA]})
	if got := must(h.BranchCommit(s.ctx, repo, "task/a"))(t); got != sha {
		t.Errorf("BranchCommit(task/a) = %q, want %s", got, sha)
	}

	replaced := must(h.PushBranch(s.ctx, repo, head, "task/a", scaffold.Tree{otherTxt: {Data: []byte("o\n")}},
		nil, "Add other.txt", contractAuthor))(t)
	if replaced == sha {
		t.Error("pushing task/a again left its head as it was")
	}
	files, _ = must2(h.ReadFiles(s.ctx, repo, "task/a", []string{readme, newTxt, otherTxt}))(t)
	wantTree(t, files, scaffold.Tree{readme: seedFiles[readme], otherTxt: {Data: []byte("o\n")}})

	if _, err := h.PushBranch(s.ctx, repo, head, "task/b", pick(seedFiles, readme), []string{missingTxt},
		"Nothing", contractAuthor); !errors.Is(err, ports.ErrEmptyChange) {
		t.Errorf("PushBranch() of no change = %v, want ErrEmptyChange", err)
	}
	if commit := must(h.BranchCommit(s.ctx, repo, ports.DefaultBranch))(t); commit != head {
		t.Errorf("pushing branches moved main to %s", commit)
	}
}

func (s suite) pushesDefaultBranch(t *testing.T) {
	h, repo, head := s.seeded(t)
	sha := must(h.PushDefaultBranch(s.ctx, repo, head, scaffold.Tree{aTxt: {Data: []byte("a\n")}},
		[]string{keepTxt}, "Add a.txt", contractAuthor))(t)
	if commit := must(h.BranchCommit(s.ctx, repo, ports.DefaultBranch))(t); commit != sha {
		t.Errorf("BranchCommit(main) = %q, want %s", commit, sha)
	}
	if history := must(h.History(s.ctx, repo, 10))(t); len(history) != 2 || history[0].Hash != sha ||
		history[1].Hash != head {
		t.Errorf("History() = %+v, want %s then %s", history, sha, head)
	}
	files, _ := must2(h.ReadFiles(s.ctx, repo, ports.DefaultBranch, []string{aTxt, "other/"}))(t)
	wantTree(t, files, scaffold.Tree{aTxt: {Data: []byte("a\n")}})

	if _, err := h.PushDefaultBranch(s.ctx, repo, head, scaffold.Tree{"b.txt": {Data: []byte("b\n")}}, nil,
		"On a moved base", contractAuthor); err == nil {
		t.Error("a push on a moved base went through")
	}
	if _, err := h.PushDefaultBranch(s.ctx, repo, sha, scaffold.Tree{aTxt: {Data: []byte("a\n")}},
		[]string{keepTxt}, "Nothing", contractAuthor); !errors.Is(err, ports.ErrEmptyChange) {
		t.Errorf("PushDefaultBranch() of no change = %v, want ErrEmptyChange", err)
	}

	if err := h.ProtectBranch(s.ctx, repo, ports.DefaultBranch, ports.BranchRule{OperatorPushes: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.PushDefaultBranch(s.ctx, repo, sha, scaffold.Tree{"b.txt": {Data: []byte("b\n")}}, nil,
		"Past the rule", contractAuthor); err != nil {
		t.Errorf("PushDefaultBranch() on a branch the operator may push to = %v", err)
	}
}

func (s suite) createsBranches(t *testing.T) {
	h, repo, head := s.seeded(t)
	if err := h.CreateBranch(s.ctx, repo, "release", head); err != nil {
		t.Fatal(err)
	}
	if has := must(h.HasBranch(s.ctx, repo, "release"))(t); !has {
		t.Error("HasBranch() of a created branch = false")
	}
	if commit := must(h.BranchCommit(s.ctx, repo, "release"))(t); commit != head {
		t.Errorf("BranchCommit() = %q, want %s", commit, head)
	}
	if err := h.CreateBranch(s.ctx, repo, "release", head); err == nil {
		t.Error("creating a branch that exists went through")
	}
	for range 2 {
		if err := h.DeleteBranch(s.ctx, repo, "release"); err != nil {
			t.Errorf("DeleteBranch() = %v", err)
		}
	}
	if has := must(h.HasBranch(s.ctx, repo, "release"))(t); has {
		t.Error("HasBranch() of a deleted branch = true")
	}
	if commit := must(h.BranchCommit(s.ctx, repo, "release"))(t); commit != "" {
		t.Errorf("BranchCommit() of a deleted branch = %q", commit)
	}
}

func (s suite) createsTags(t *testing.T) {
	h, repo, head := s.seeded(t)
	if commit := must(h.GetTag(s.ctx, repo, "v1.0.0"))(t); commit != "" {
		t.Errorf("GetTag() of a missing tag = %q", commit)
	}
	if err := h.CreateTag(s.ctx, repo, "v1.0.0", head, "Release v1.0.0"); err != nil {
		t.Fatal(err)
	}
	if commit := must(h.GetTag(s.ctx, repo, "v1.0.0"))(t); commit != head {
		t.Errorf("GetTag() = %q, want %s", commit, head)
	}
	if err := h.CreateTag(s.ctx, repo, "v1.0.0", head, "Again"); err == nil {
		t.Error("creating a tag that exists went through")
	}
}

func (s suite) protects(t *testing.T) {
	h, repo, _ := s.seeded(t)
	for _, rule := range []ports.BranchRule{{StatusContexts: []string{checkGlob}},
		{StatusContexts: []string{checkGlob}, ProtectedPaths: []string{docsDir, readme}, OperatorPushes: true}} {
		if err := h.ProtectBranch(s.ctx, repo, ports.DefaultBranch, rule); err != nil {
			t.Errorf("ProtectBranch(%+v) = %v", rule, err)
		}
	}
	for range 2 {
		if err := h.ProtectTags(s.ctx, repo, "v*"); err != nil {
			t.Errorf("ProtectTags() = %v", err)
		}
	}
}

func (s suite) mergesPullRequest(t *testing.T) {
	h, repo, head := s.seeded(t)
	sha := must(h.PushBranch(s.ctx, repo, head, "task/a", scaffold.Tree{newTxt: {Data: []byte("new\n")}}, nil,
		"Add new.txt", contractAuthor))(t)
	pr := must(h.OpenPullRequest(s.ctx, repo, "task/a", ports.DefaultBranch, "Add new.txt", "Body"))(t)
	again := must(h.OpenPullRequest(s.ctx, repo, "task/a", ports.DefaultBranch, "Add new.txt", "Body"))(t)
	if again.Number != pr.Number || pr.HeadSHA != sha || pr.Head != "task/a" || pr.Merged {
		t.Errorf("OpenPullRequest() = %+v, then %+v; want one open pull request at %s", pr, again, sha)
	}
	if _, err := h.GetPullRequest(s.ctx, repo, pr.Number+100); !errors.Is(err, ports.ErrPullRequestNotFound) {
		t.Errorf("GetPullRequest() of a missing pull request = %v, want ErrPullRequestNotFound", err)
	}
	mergeable(s.ctx, t, h, repo, pr.Number)
	if err := h.MergePullRequest(s.ctx, repo, pr.Number, head, "Add new.txt", ""); err == nil {
		t.Error("a merge on a stale head went through")
	}
	if err := h.MergePullRequest(s.ctx, repo, pr.Number, sha, "Add new.txt", "Squashed"); err != nil {
		t.Fatal(err)
	}
	merged := must(h.GetPullRequest(s.ctx, repo, pr.Number))(t)
	mainHead := must(h.BranchCommit(s.ctx, repo, ports.DefaultBranch))(t)
	if !merged.Merged || merged.MergeCommit == "" || merged.MergeCommit != mainHead {
		t.Errorf("GetPullRequest() after the merge = %+v, want merged as main's head %s", merged, mainHead)
	}
	files, _ := must2(h.ReadFiles(s.ctx, repo, ports.DefaultBranch, []string{newTxt, readme}))(t)
	wantTree(t, files, scaffold.Tree{newTxt: {Data: []byte("new\n")}, readme: seedFiles[readme]})
	eventually(s.ctx, t, "task/a deleted", func() bool { return !must(h.HasBranch(s.ctx, repo, "task/a"))(t) })
}

func (s suite) forcesMerge(t *testing.T) {
	h, repo, head := s.seeded(t)
	if err := h.ProtectBranch(s.ctx, repo, ports.DefaultBranch,
		ports.BranchRule{ProtectedPaths: []string{docsDir}}); err != nil {
		t.Fatal(err)
	}
	sha := must(h.PushBranch(s.ctx, repo, head, "task/docs", scaffold.Tree{docsA: {Data: []byte("A\n")}},
		nil, "Edit docs", contractAuthor))(t)
	pr := must(h.OpenPullRequest(s.ctx, repo, "task/docs", ports.DefaultBranch, "Edit docs", ""))(t)
	mergeable(s.ctx, t, h, repo, pr.Number)
	if err := h.MergePullRequest(s.ctx, repo, pr.Number, sha, "Edit docs", ""); err == nil {
		t.Fatal("a merge that changes a protected path went through")
	}
	if err := h.ForceMergePullRequest(s.ctx, repo, pr.Number, sha, "Edit docs", ""); err != nil {
		t.Fatalf("ForceMergePullRequest() = %v", err)
	}
	if merged := must(h.GetPullRequest(s.ctx, repo, pr.Number))(t); !merged.Merged {
		t.Errorf("GetPullRequest() after a forced merge = %+v, want merged", merged)
	}
	files, _ := must2(h.ReadFiles(s.ctx, repo, ports.DefaultBranch, []string{docsA}))(t)
	wantTree(t, files, scaffold.Tree{docsA: {Data: []byte("A\n")}})
}

func (s suite) updatesPullRequest(t *testing.T) {
	h, repo, head := s.seeded(t)
	sha := must(h.PushBranch(s.ctx, repo, head, "task/a", scaffold.Tree{aTxt: {Data: []byte("a\n")}}, nil,
		"Add a.txt", contractAuthor))(t)
	a := must(h.OpenPullRequest(s.ctx, repo, "task/a", ports.DefaultBranch, "A", ""))(t)
	must(h.PushBranch(s.ctx, repo, head, "task/c", scaffold.Tree{readme: {Data: []byte("branch\n")}}, nil,
		"Edit the README", contractAuthor))(t)
	c := must(h.OpenPullRequest(s.ctx, repo, "task/c", ports.DefaultBranch, "C", ""))(t)
	mergeable(s.ctx, t, h, repo, a.Number)
	mergeable(s.ctx, t, h, repo, c.Number)
	must(h.PushDefaultBranch(s.ctx, repo, head, scaffold.Tree{readme: {Data: []byte("main\n")}}, nil,
		"Edit the README on main", contractAuthor))(t)

	// The host may learn that main moved in the background: update until
	// the head moves.
	eventually(s.ctx, t, "updated", func() bool {
		if err := h.UpdatePullRequest(s.ctx, repo, a.Number); err != nil {
			t.Fatalf("UpdatePullRequest() = %v", err)
		}
		return must(h.GetPullRequest(s.ctx, repo, a.Number))(t).HeadSHA != sha
	})
	files, _ := must2(h.ReadFiles(s.ctx, repo, "task/a", []string{aTxt, readme}))(t)
	wantTree(t, files, scaffold.Tree{aTxt: {Data: []byte("a\n")}, readme: {Data: []byte("main\n")}})

	if err := h.UpdatePullRequest(s.ctx, repo, c.Number); !errors.Is(err, ports.ErrConflict) {
		t.Errorf("UpdatePullRequest() of a conflicting branch = %v, want ErrConflict", err)
	}
}

func (s suite) listsPullRequestFiles(t *testing.T) {
	h, repo, head := s.seeded(t)
	must(h.PushBranch(s.ctx, repo, head, "task/r", scaffold.Tree{
		"docs/renamed.md": seedFiles[docsB], newTxt: {Data: []byte("new\n")},
	}, []string{docsB}, "Rename b.md", contractAuthor))(t)
	pr := must(h.OpenPullRequest(s.ctx, repo, "task/r", ports.DefaultBranch, "Rename", ""))(t)
	files := must(h.PullRequestFiles(s.ctx, repo, pr.Number))(t)
	slices.Sort(files)
	if want := []string{"docs/renamed.md", docsB, newTxt}; !slices.Equal(files, want) {
		t.Errorf("PullRequestFiles() = %q, want %q", files, want)
	}
}

func (s suite) combinesStatuses(t *testing.T) {
	h, repo, head := s.seeded(t)
	if st := must(h.CombinedStatus(s.ctx, repo, head))(t); st.State != "" || len(st.Contexts) != 0 {
		t.Errorf("CombinedStatus() before any check = %+v, want none", st)
	}
	s.env.SetStatus(t, repo, head, "check/a", ports.CheckSuccess)
	if st := must(h.CombinedStatus(s.ctx, repo, head))(t); st.State != ports.CheckSuccess {
		t.Errorf("CombinedStatus() of one passing check = %+v, want success", st)
	}
	s.env.SetStatus(t, repo, head, "check/b", ports.CheckFailure)
	st := must(h.CombinedStatus(s.ctx, repo, head))(t)
	want := map[string]string{"check/a": ports.CheckSuccess, "check/b": ports.CheckFailure}
	if st.State != ports.CheckFailure || !maps.Equal(st.Contexts, want) {
		t.Errorf("CombinedStatus() = %+v, want failure with %v", st, want)
	}
}

func (s suite) readsRelease(t *testing.T) {
	h, repo, head := s.seeded(t)
	if rel := must(h.GetRelease(s.ctx, repo, "v1.0.0"))(t); rel != nil {
		t.Errorf("GetRelease() of a tag with none = %+v", rel)
	}
	s.env.Release(t, repo, "v1.0.0", head, "Notes", "app.tar.gz", "the app")
	rel := must(h.GetRelease(s.ctx, repo, "v1.0.0"))(t)
	if rel == nil || rel.Tag != "v1.0.0" || rel.Body != "Notes" || len(rel.Assets) != 1 ||
		rel.Assets[0].Name != "app.tar.gz" || rel.Assets[0].Size != int64(len("the app")) {
		t.Fatalf("GetRelease() = %+v, want v1.0.0 with app.tar.gz", rel)
	}
	body, size, err := h.OpenReleaseAsset(s.ctx, repo, rel.Assets[0])
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != "the app" || size != int64(len("the app")) {
		t.Errorf("OpenReleaseAsset() read %q, size %d, %v; want %q", data, size, err, "the app")
	}
	for _, set := range []struct{ body, want string }{{"", "Notes"}, {"New notes", "New notes"}} {
		if err := h.SetReleaseBody(s.ctx, repo, rel.ID, set.body); err != nil {
			t.Fatal(err)
		}
		if got := must(h.GetRelease(s.ctx, repo, "v1.0.0"))(t); got.Body != set.want {
			t.Errorf("SetReleaseBody(%q) left %q, want %q", set.body, got.Body, set.want)
		}
	}
}

func (s suite) readsSource(t *testing.T) {
	h, repo, head := s.seeded(t)
	if err := h.CreateTag(s.ctx, repo, "v1.0.0", head, "Release v1.0.0"); err != nil {
		t.Fatal(err)
	}
	must(h.PushDefaultBranch(s.ctx, repo, head, scaffold.Tree{"later.txt": {Data: []byte("later\n")}}, nil,
		"After the tag", contractAuthor))(t)
	got := scaffold.Tree{}
	err := h.ReadSource(s.ctx, repo, "v1.0.0", func(f ports.SourceFile, r io.Reader) error {
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if f.Size != int64(len(data)) {
			t.Errorf("%s: Size %d, read %d bytes", f.Path, f.Size, len(data))
		}
		if f.Mode != ports.SourcePlain && f.Mode != ports.SourceExecutable {
			t.Errorf("%s: mode %s", f.Path, f.Mode)
		}
		got[f.Path] = scaffold.File{Data: data, Executable: f.Mode == ports.SourceExecutable}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	wantTree(t, got, seedFiles)

	stop := errors.New("stop")
	if err := h.ReadSource(s.ctx, repo, "v1.0.0", func(ports.SourceFile, io.Reader) error {
		return stop
	}); !errors.Is(err, stop) {
		t.Errorf("ReadSource() stopped by fn = %v, want fn's error", err)
	}
}

// mergeable polls pull request number until the host says it can merge: a
// host may test it against its base in the background.
func mergeable(ctx context.Context, t *testing.T, h ports.GitHost, repo ports.Repository, number int) {
	t.Helper()
	eventually(ctx, t, "mergeable", func() bool { return must(h.GetPullRequest(ctx, repo, number))(t).Mergeable })
}

// eventually polls ok until it holds, failing t after waitFor.
func eventually(ctx context.Context, t *testing.T, what string, ok func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, waitFor)
	defer cancel()
	for !ok() {
		select {
		case <-ctx.Done():
			t.Fatalf("never %s", what)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// must returns a call's value, failing t on its error.
func must[V any](v V, err error) func(t *testing.T) V {
	return func(t *testing.T) V {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

// must2 is must of a call with two values.
func must2[V, W any](v V, w W, err error) func(t *testing.T) (V, W) {
	return func(t *testing.T) (V, W) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v, w
	}
}

// pick is the files of tree at paths.
func pick(tree scaffold.Tree, paths ...string) scaffold.Tree {
	out := scaffold.Tree{}
	for _, p := range paths {
		out[p] = tree[p]
	}
	return out
}

// wantTree fails t unless got holds want's files, content and executable bit,
// and no other.
func wantTree(t *testing.T, got, want scaffold.Tree) {
	t.Helper()
	for p, w := range want {
		g, ok := got[p]
		if !ok {
			t.Errorf("%s missing", p)
		} else if string(g.Data) != string(w.Data) || g.Executable != w.Executable {
			t.Errorf("%s = %q (executable %t), want %q (executable %t)", p, g.Data, g.Executable, w.Data, w.Executable)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("%s read, want it left out", p)
		}
	}
}
