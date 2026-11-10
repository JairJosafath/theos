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

package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/JairJosafath/theos/internal/adapters/github/githubtest"
	"github.com/JairJosafath/theos/internal/ports"
	"github.com/JairJosafath/theos/internal/scaffold"
)

// shop is the repository acme/shop on a fake GitHub, with one commit, and
// the operator's host on it: the account that administers the repository.
type shop struct {
	ctx  context.Context
	gh   *githubtest.Server
	h    Host
	repo ports.Repository
	head string
}

var shopAuthor = ports.Signature{Name: "Ada", Email: "ada@example.com"}

const (
	shopReadme = "README.md"
	shopDocs   = "docs/a.md"
	checkUnit  = "check / unit"
	checkAny   = "check / *"
)

func newShop(t *testing.T) shop {
	t.Helper()
	s := shop{ctx: context.Background(), gh: githubtest.New(t, "ada", hostToken)}
	s.gh.AddOrg("acme")
	h, err := (Client{}).Host(ports.Config{URL: s.gh.URL(), Owner: "acme", Credentials: ports.Credentials{Token: hostToken}})
	if err != nil {
		t.Fatal(err)
	}
	s.h = h
	if s.repo, err = h.EnsureRepository(s.ctx, "shop", "The shop"); err != nil {
		t.Fatal(err)
	}
	if s.head, err = h.PushInitialCommit(s.ctx, s.repo, scaffold.Tree{shopReadme: {Data: []byte("hello\n")},
		shopDocs: {Data: []byte("a\n")}}, "Scaffold\n", shopAuthor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if calls := s.gh.Unserved(); len(calls) > 0 {
			t.Errorf("the adapter made calls the fake GitHub doesn't serve: %q", calls)
		}
	})
	return s
}

// as returns the host of another account: one that may write to the
// repository and administers nothing, as a collaborator does.
func (s shop) as(t *testing.T, login string) Host {
	t.Helper()
	token := "ghp_" + login
	s.gh.AddUser(login, token)
	h, err := (Client{}).Host(ports.Config{URL: s.gh.URL(), Owner: "acme", Credentials: ports.Credentials{Token: token}})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// open pushes branch with files on main's head and opens its pull request.
func (s shop) open(t *testing.T, branch string, files scaffold.Tree, removes ...string) ports.PullRequest {
	t.Helper()
	main, err := s.h.BranchCommit(s.ctx, s.repo, ports.DefaultBranch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.h.PushBranch(s.ctx, s.repo, main, branch, files, removes, "Change "+branch, shopAuthor); err != nil {
		t.Fatal(err)
	}
	pr, err := s.h.OpenPullRequest(s.ctx, s.repo, branch, ports.DefaultBranch, "Change "+branch, "")
	if err != nil {
		t.Fatal(err)
	}
	return pr
}

// mergeRule is the rule TestMergeRuleOnGitHub protects main with.
var mergeRule = ports.BranchRule{StatusContexts: []string{checkAny}, ProtectedPaths: []string{"docs/", "Makefile"}}

func (s shop) merge(pr ports.PullRequest) error {
	return s.h.MergePullRequest(s.ctx, s.repo, pr.Number, pr.HeadSHA, "Merge", "")
}

func (s shop) merged(t *testing.T, pr ports.PullRequest) bool {
	t.Helper()
	got, err := s.h.GetPullRequest(s.ctx, s.repo, pr.Number)
	if err != nil {
		t.Fatal(err)
	}
	return got.Merged
}

// commit pushes a commit of its own onto main and returns it.
func (s shop) commit(t *testing.T, name string) string {
	t.Helper()
	main, _ := s.h.BranchCommit(s.ctx, s.repo, ports.DefaultBranch)
	sha, err := s.h.PushDefaultBranch(s.ctx, s.repo, main, scaffold.Tree{name: {Data: []byte(name)}}, nil, "Add "+name,
		shopAuthor)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

func (s shop) setStatus(t *testing.T, sha, check, state, page string) {
	t.Helper()
	if err := s.h.SetCommitStatus(s.ctx, s.repo, sha, ports.CommitStatus{Context: check, State: state,
		TargetURL: page}); err != nil {
		t.Fatal(err)
	}
}

func (s shop) passed(sha string) {
	s.gh.AddCheckRun("acme", "shop", sha, checkUnit, "completed", "success")
}

// TestBranchRulesetsOnGitHub covers #75: the ruleset the adapter makes of
// each BranchRule, and of a tag pattern.
func TestBranchRulesetsOnGitHub(t *testing.T) {
	checked := ports.BranchRule{StatusContexts: []string{checkAny}, ProtectedPaths: []string{".github/", "Makefile"}}
	pushes := checked
	pushes.OperatorPushes = true
	for _, tt := range []struct {
		name   string
		rule   ports.BranchRule
		bypass string
	}{
		{"a fence alone lets the operator in by pull request only", ports.BranchRule{}, bypassPullRequest},
		{"checks and protected paths change no rule of the ruleset", checked, bypassPullRequest},
		{"a rule that lets the operator push lets it bypass always", pushes, bypassAlways},
		{"so does one with nothing else", ports.BranchRule{OperatorPushes: true}, bypassAlways},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := branchRuleset("main", tt.rule)
			digest, named := ruleDigest(got.Name, "main")
			want := ruleset{Name: "theos: main (rule " + digest + ")", Target: "branch", Enforcement: "active",
				Conditions:   rulesetConditions{RefName: refNames{Include: []string{"refs/heads/main"}, Exclude: []string{}}},
				BypassActors: []bypassActor{{ActorID: 5, ActorType: "RepositoryRole", BypassMode: tt.bypass}},
				Rules:        []rulesetRule{{Type: "update"}, {Type: "deletion"}, {Type: "non_fast_forward"}}}
			if !named || !reflect.DeepEqual(got, want) {
				t.Errorf("branchRuleset() = %+v, want %+v", got, want)
			}
			if kept := (storedRule{tt.rule.StatusContexts, tt.rule.ProtectedPaths}).digest(); digest != kept {
				t.Errorf("the ruleset names rule %s, the rule kept is %s", digest, kept)
			}
		})
	}

	t.Run("the name carries the rule's checks and paths, not who pushes", func(t *testing.T) {
		name := func(rule ports.BranchRule) string { return branchRuleset("main", rule).Name }
		if name(checked) != name(pushes) {
			t.Errorf("%q and %q differ, and the rule a merge is held to is the same", name(checked), name(pushes))
		}
		weaker := ports.BranchRule{StatusContexts: checked.StatusContexts}
		if name(checked) == name(weaker) || name(checked) == name(ports.BranchRule{}) {
			t.Errorf("rules that differ share the name %q", name(checked))
		}
		for name, branch := range map[string]string{
			"theos: main (rule 0123456789ab)":                     "main",
			"theos: a (rule x) (rule 0123456789ab)":               "a (rule x)",
			"theos: release/1.x (rule ffffffffffff)":              "release/1.x",
			"theos: main (rule 0123456789ab) (rule 0123456789ab)": "main (rule 0123456789ab)",
		} {
			if _, ok := ruleDigest(name, branch); !ok {
				t.Errorf("%q is not read as the ruleset of %q", name, branch)
			}
		}
		for name, branch := range map[string]string{
			"theos: main":                             "main",
			"theos: main (rule 0123456789AB)":         "main",
			"theos: main (rule 0123456789abc)":        "main",
			"theos: maintenance (rule 0123456789ab)":  "main",
			"theos: tags v*":                          "tags v*",
			"theos: main (rule x) (rule 0123456789ab": "main",
			"mine: main (rule 0123456789ab)":          "main",
		} {
			if digest, ok := ruleDigest(name, branch); ok {
				t.Errorf("%q is read as the ruleset of %q, rule %s", name, branch, digest)
			}
		}
	})

	t.Run("a tag pattern is the operator's to make, move and delete", func(t *testing.T) {
		want := ruleset{Name: "theos: tags v*", Target: "tag", Enforcement: "active",
			Conditions:   rulesetConditions{RefName: refNames{Include: []string{"refs/tags/v*"}, Exclude: []string{}}},
			BypassActors: []bypassActor{{ActorID: 5, ActorType: "RepositoryRole", BypassMode: bypassAlways}},
			Rules:        []rulesetRule{{Type: "creation"}, {Type: "update"}, {Type: "deletion"}}}
		if got := tagRuleset("v*"); !reflect.DeepEqual(got, want) {
			t.Errorf("tagRuleset() = %+v, want %+v", got, want)
		}
	})

	t.Run("GitHub takes lists, never null", func(t *testing.T) {
		data, err := json.Marshal(branchRuleset("main", ports.BranchRule{}))
		if err != nil || strings.Contains(string(data), "null") {
			t.Errorf("the ruleset is sent as %s, %v", data, err)
		}
	})
}

// TestProtectOnGitHub covers #75: what ProtectBranch and ProtectTags leave on
// GitHub, again and for a second branch, and who may.
func TestProtectOnGitHub(t *testing.T) {
	s := newShop(t)
	first := ports.BranchRule{StatusContexts: []string{checkAny}}
	second := ports.BranchRule{StatusContexts: []string{checkAny}, ProtectedPaths: []string{"docs/"}, OperatorPushes: true}
	release := ports.BranchRule{ProtectedPaths: []string{"Makefile"}}
	kept := func() map[string]storedRule {
		value, ok := s.gh.Variable("acme", "shop", rulesVariable)
		if !ok {
			t.Fatalf("the repository has no variable %s", rulesVariable)
		}
		rules := map[string]storedRule{}
		if err := json.Unmarshal([]byte(value), &rules); err != nil {
			t.Fatalf("%s holds %q: %v", rulesVariable, value, err)
		}
		return rules
	}

	if err := s.h.ProtectBranch(s.ctx, s.repo, ports.DefaultBranch, first); err != nil {
		t.Fatal(err)
	}
	made := s.gh.Rulesets("acme", "shop")
	if len(made) != 1 || made[0].Name != branchRuleset("main", first).Name ||
		made[0].BypassActors[0].BypassMode != bypassPullRequest {
		t.Fatalf("the rulesets after the first rule: %+v", made)
	}

	t.Run("a second rule replaces the first in the one ruleset and in the variable", func(t *testing.T) {
		if err := s.h.ProtectBranch(s.ctx, s.repo, ports.DefaultBranch, second); err != nil {
			t.Fatal(err)
		}
		now := s.gh.Rulesets("acme", "shop")
		if len(now) != 1 || now[0].ID != made[0].ID || now[0].Name != branchRuleset("main", second).Name ||
			now[0].Name == made[0].Name || now[0].BypassActors[0].BypassMode != bypassAlways {
			t.Errorf("the rulesets after the second rule: %+v, want the first replaced", now)
		}
		want := map[string]storedRule{"main": {second.StatusContexts, second.ProtectedPaths}}
		if got := kept(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s holds %+v, want %+v", rulesVariable, got, want)
		}
	})

	t.Run("a second branch and the tags get rulesets of their own, once", func(t *testing.T) {
		for range 2 {
			if err := s.h.ProtectBranch(s.ctx, s.repo, "release", release); err != nil {
				t.Fatal(err)
			}
			if err := s.h.ProtectTags(s.ctx, s.repo, "v*"); err != nil {
				t.Fatal(err)
			}
		}
		made := s.gh.Rulesets("acme", "shop")
		names := make([]string, 0, len(made))
		for _, rs := range made {
			names = append(names, rs.Name)
		}
		want := []string{branchRuleset("main", second).Name, branchRuleset("release", release).Name, "theos: tags v*"}
		if !slices.Equal(names, want) {
			t.Errorf("the rulesets are %q, want %q", names, want)
		}
		if got := kept(); len(got) != 2 || !reflect.DeepEqual(got["release"], storedRule{nil, release.ProtectedPaths}) {
			t.Errorf("%s holds %+v, want main's rule and release's", rulesVariable, got)
		}
	})

	t.Run("the rule read back is the one protected, and none for a branch never fenced", func(t *testing.T) {
		got, err := s.h.(*host).branchRule(s.ctx, s.repo, ports.DefaultBranch)
		want := ports.BranchRule{StatusContexts: second.StatusContexts, ProtectedPaths: second.ProtectedPaths}
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("branchRule(main) = %+v, %v; want %+v", got, err, want)
		}
		if got, err := s.h.(*host).branchRule(s.ctx, s.repo, "task/a"); err != nil || !reflect.DeepEqual(got, ports.BranchRule{}) {
			t.Errorf("branchRule() of a branch never fenced = %+v, %v; want none", got, err)
		}
	})

	t.Run("an account that administers nothing can't fence a branch", func(t *testing.T) {
		before := s.gh.Rulesets("acme", "shop")
		grace := s.as(t, "grace")
		err := grace.ProtectBranch(s.ctx, s.repo, "task/a", ports.BranchRule{})
		if err == nil || !strings.Contains(err.Error(), "HTTP 403") {
			t.Errorf("ProtectBranch() without administration = %v, want GitHub's 403", err)
		}
		if err := grace.ProtectTags(s.ctx, s.repo, "x*"); err == nil || !strings.Contains(err.Error(), "HTTP 403") {
			t.Errorf("ProtectTags() without administration = %v, want GitHub's 403", err)
		}
		if after := s.gh.Rulesets("acme", "shop"); !reflect.DeepEqual(after, before) {
			t.Errorf("the rulesets changed: %+v", after)
		}
	})
}

// TestMergeRuleOnGitHub covers #75: GitHub holds no pull request to a
// branch's protected paths or to checks named by a pattern, and lets the
// operator past its own ruleset, so MergePullRequest checks the rule itself
// and ForceMergePullRequest doesn't.
func TestMergeRuleOnGitHub(t *testing.T) {
	s := newShop(t)
	if err := s.h.ProtectBranch(s.ctx, s.repo, ports.DefaultBranch, mergeRule); err != nil {
		t.Fatal(err)
	}
	t.Run("a check must have passed on the head: none, a running one and a failed one refuse", s.mergeNeedsCheck)
	t.Run("a commit status passes a rule as a check run does", s.mergeTakesStatus)
	t.Run("a protected path refuses, by name, a rename's old path too; a forced merge goes through", s.mergeRefusesProtectedPath)
	t.Run("a forced merge needs no check either, and still the head it was given", s.forcedMerge)
	// Anyone with write access sets an Actions variable through GitHub's
	// API; only an administrator a ruleset, whose name says which rule holds.
	t.Run("a rule someone weakened or removed refuses the merge until the branch is protected again", s.tamperedRule)
	t.Run("a branch never protected merges unchecked", s.unprotectedBranch)
}

// TestPullRequestsOnGitHub covers #75: reviews, comments, a branch behind
// its base, a pull request's size and commits, and closing one.
func TestPullRequestsOnGitHub(t *testing.T) {
	s := newShop(t)
	t.Run("reviews: who, what, of which commit; stale, dismissed and unsent ones", s.reviews)
	t.Run("a comment lands on the pull request", s.comment)
	t.Run("its size, behind its base until updated, its commits before and after the merge", s.sizeBehindCommits)
	t.Run("a closed pull request is closed, not merged, and its branch opens a new one", s.closed)
	t.Run("a branch with nothing its base lacks opens none, with GitHub's words", s.emptyBranch)
	t.Run("a pull request the repository lacks is not found, whatever is asked of it", s.missingPull)
}

// TestStatusesOnGitHub covers #75: commit statuses and check runs read as
// one list of checks by context.
func TestStatusesOnGitHub(t *testing.T) {
	s := newShop(t)
	t.Run("a check run's state: pending until done, then its conclusion", s.checkRunStates)
	t.Run("the worst check decides, and its page is the one to open", s.worstCheck)
	t.Run("the latest status and run of a context count, by branch as by commit", s.latestCheck)
	t.Run("a description longer than GitHub takes is cut, not refused", s.longDescription)
	t.Run("more checks than a page are all read", s.manyChecks)
	t.Run("a commit the repository lacks is an error", s.missingCommit)
}

// TestReleasesOnGitHub covers #75: a draft release, an asset that is not the
// size its release says, and the source of a tag the repository lacks.
func TestReleasesOnGitHub(t *testing.T) {
	s := newShop(t)

	t.Run("a draft has no tag yet and is found all the same", func(t *testing.T) {
		if err := s.gh.AddRelease("acme", "shop", "v2.0.0", s.head, "Soon", "app.tar.gz", "the app", true); err != nil {
			t.Fatal(err)
		}
		rel, err := s.h.GetRelease(s.ctx, s.repo, "v2.0.0")
		if err != nil || rel == nil || !rel.Draft || rel.Tag != "v2.0.0" || rel.Body != "Soon" || len(rel.Assets) != 1 {
			t.Fatalf("GetRelease() of a draft = %+v, %v", rel, err)
		}
		if tag, _ := s.h.GetTag(s.ctx, s.repo, "v2.0.0"); tag != "" {
			t.Errorf("the draft's tag exists, at %s", tag)
		}
		if other, err := s.h.GetRelease(s.ctx, s.repo, "v2.0.1"); err != nil || other != nil {
			t.Errorf("GetRelease() of a tag with no release = %+v, %v", other, err)
		}
	})

	t.Run("an asset that is not the size its release says is refused", func(t *testing.T) {
		rel, err := s.h.GetRelease(s.ctx, s.repo, "v2.0.0")
		if err != nil || rel == nil {
			t.Fatal(rel, err)
		}
		asset := rel.Assets[0]
		asset.Size++
		if body, _, err := s.h.OpenReleaseAsset(s.ctx, s.repo, asset); err == nil {
			_ = body.Close()
			t.Error("OpenReleaseAsset() of a longer asset than GitHub sends went through")
		} else if !strings.Contains(err.Error(), "is 7 bytes, the release says 8") {
			t.Errorf("OpenReleaseAsset() = %v, want the two sizes", err)
		}
		asset.ID += 1000
		if _, _, err := s.h.OpenReleaseAsset(s.ctx, s.repo, asset); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("OpenReleaseAsset() of an asset GitHub lacks = %v, want the 404", err)
		}
	})

	t.Run("a body that is short or long of its size fails as it is read", func(t *testing.T) {
		for name, tt := range map[string]struct {
			sent string
			size int64
			want string
		}{
			"short": {"the ap", 7, "ended 1 bytes short"},
			"long":  {"the app!", 7, "is longer than the release says"},
		} {
			body := &sizedBody{body: io.NopCloser(strings.NewReader(tt.sent)), left: tt.size, name: "app.tar.gz of acme/shop"}
			if _, err := io.ReadAll(body); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("%s: reading it = %v, want %q", name, err, tt.want)
			}
		}
	})

	t.Run("notes of a release GitHub lacks, and the source of a tag it lacks, fail with its words", func(t *testing.T) {
		if err := s.h.SetReleaseBody(s.ctx, s.repo, 999999, "Notes"); err == nil || !strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("SetReleaseBody() of a missing release = %v, want the 404", err)
		}
		err := s.h.ReadSource(s.ctx, s.repo, "v9.9.9", func(ports.SourceFile, io.Reader) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "the archive of acme/shop at v9.9.9") ||
			!strings.Contains(err.Error(), "HTTP 404") {
			t.Errorf("ReadSource() of a missing tag = %v, want the 404 and whose archive it was", err)
		}
	})
}

func (s shop) mergeNeedsCheck(t *testing.T) {
	pr := s.open(t, "task/a", scaffold.Tree{"src/a.txt": {Data: []byte("a\n")}})
	for _, run := range []struct{ why, status, conclusion string }{
		{"no check at all", "", ""},
		{"a check that runs", "in_progress", ""},
		{"a check that failed", "completed", "failure"},
		{"a check that was cancelled", "completed", "cancelled"},
	} {
		if run.status != "" {
			s.gh.AddCheckRun("acme", "shop", pr.HeadSHA, checkUnit, run.status, run.conclusion)
		}
		err := s.merge(pr)
		if err == nil || !strings.Contains(err.Error(), "no check matching "+checkAny+" has passed on "+pr.HeadSHA) {
			t.Errorf("MergePullRequest() with %s = %v, want it refused by the rule", run.why, err)
		}
	}
	if s.merged(t, pr) {
		t.Fatal("a refused merge merged")
	}
	s.passed(pr.HeadSHA)
	if err := s.merge(pr); err != nil {
		t.Fatalf("MergePullRequest() once the check passed = %v", err)
	}
	if !s.merged(t, pr) {
		t.Error("the pull request is not merged")
	}
	if err := s.merge(pr); err != nil {
		t.Errorf("MergePullRequest() of a pull request merged at that head = %v, want it done", err)
	}
}

func (s shop) mergeTakesStatus(t *testing.T) {
	pr := s.open(t, "task/status", scaffold.Tree{"src/s.txt": {Data: []byte("s\n")}})
	if err := s.h.SetCommitStatus(s.ctx, s.repo, pr.HeadSHA, ports.CommitStatus{Context: "check / lint",
		State: ports.CheckSuccess}); err != nil {
		t.Fatal(err)
	}
	if err := s.merge(pr); err != nil {
		t.Errorf("MergePullRequest() with a passing status = %v", err)
	}
}

func (s shop) mergeRefusesProtectedPath(t *testing.T) {
	pr := s.open(t, "task/docs", scaffold.Tree{"Makefile": {Data: []byte("all:\n")}, "src/b.txt": {Data: []byte("b\n")},
		"notes/a.md": {Data: []byte("a\n")}}, shopDocs)
	s.passed(pr.HeadSHA)
	err := s.merge(pr)
	if err == nil || !strings.Contains(err.Error(), "it changes Makefile, "+shopDocs+", which no pull request may") {
		t.Fatalf("MergePullRequest() of a protected path = %v, want the paths named", err)
	}
	if strings.Contains(err.Error(), "src/b.txt") || strings.Contains(err.Error(), "notes/a.md,") {
		t.Errorf("%v names a path that is not protected", err)
	}
	if s.merged(t, pr) {
		t.Fatal("a refused merge merged")
	}
	if err := s.h.ForceMergePullRequest(s.ctx, s.repo, pr.Number, pr.HeadSHA, "Merge", ""); err != nil {
		t.Fatalf("ForceMergePullRequest() = %v", err)
	}
	if !s.merged(t, pr) {
		t.Error("the forced merge did not merge")
	}
}

func (s shop) forcedMerge(t *testing.T) {
	pr := s.open(t, "task/forced", scaffold.Tree{"src/f.txt": {Data: []byte("f\n")}})
	err := s.h.ForceMergePullRequest(s.ctx, s.repo, pr.Number, s.head, "Merge", "")
	if err == nil || !strings.Contains(err.Error(), "it moved since it was checked") {
		t.Errorf("ForceMergePullRequest() on a stale head = %v", err)
	}
	if err := s.h.ForceMergePullRequest(s.ctx, s.repo, pr.Number, pr.HeadSHA, "Merge", ""); err != nil {
		t.Errorf("ForceMergePullRequest() = %v", err)
	}
}

func (s shop) tamperedRule(t *testing.T) {
	pr := s.open(t, "task/sneak", scaffold.Tree{"docs/b.md": {Data: []byte("b\n")}})
	s.passed(pr.HeadSHA)
	grace := s.as(t, "grace").(*host)
	variable := grace.variablePath(s.repo) + "/" + rulesVariable
	tamper := map[string]func() (int, []byte, error){
		"weakened": func() (int, []byte, error) {
			return grace.do(s.ctx, http.MethodPatch, variable, map[string]string{"name": rulesVariable,
				"value": `{"main":{"statusContexts":["check / *"]}}`})
		},
		"emptied": func() (int, []byte, error) {
			return grace.do(s.ctx, http.MethodPatch, variable, map[string]string{"name": rulesVariable, "value": "{}"})
		},
		"broken": func() (int, []byte, error) {
			return grace.do(s.ctx, http.MethodPatch, variable, map[string]string{"name": rulesVariable, "value": "no JSON"})
		},
		"removed": func() (int, []byte, error) { return grace.do(s.ctx, http.MethodDelete, variable, nil) },
	}
	for _, how := range slices.Sorted(maps.Keys(tamper)) {
		if status, data, err := tamper[how](); err != nil || status != http.StatusNoContent {
			t.Fatalf("the fake refused a collaborator's write of the variable (%s): HTTP %d %s, %v", how, status, data, err)
		}
		err := s.merge(pr)
		if err == nil || !strings.Contains(err.Error(), "is not the one its ruleset names") {
			t.Errorf("MergePullRequest() with the rule %s = %v, want it refused", how, err)
		}
		if s.merged(t, pr) {
			t.Fatalf("the pull request merged with the rule %s", how)
		}
		if err := s.h.ProtectBranch(s.ctx, s.repo, ports.DefaultBranch, mergeRule); err != nil {
			t.Fatalf("ProtectBranch() over a rule %s = %v", how, err)
		}
		err = s.merge(pr)
		if err == nil || !strings.Contains(err.Error(), "it changes docs/b.md") {
			t.Errorf("MergePullRequest() with the rule protected again = %v, want the rule's own refusal", err)
		}
	}
}

func (s shop) unprotectedBranch(t *testing.T) {
	if err := s.h.CreateBranch(s.ctx, s.repo, "next", s.head); err != nil {
		t.Fatal(err)
	}
	if _, err := s.h.PushBranch(s.ctx, s.repo, s.head, "task/next", scaffold.Tree{shopDocs: {Data: []byte("A\n")}},
		nil, "Edit docs", shopAuthor); err != nil {
		t.Fatal(err)
	}
	pr, err := s.h.OpenPullRequest(s.ctx, s.repo, "task/next", "next", "Edit docs", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.merge(pr); err != nil {
		t.Errorf("MergePullRequest() into a branch with no rule = %v", err)
	}
}

func (s shop) reviews(t *testing.T) {
	grace := s.as(t, "grace")
	pr := s.open(t, "task/review", scaffold.Tree{"src/a.txt": {Data: []byte("a\n")}})
	err := s.h.ReviewPullRequest(s.ctx, s.repo, pr.Number, pr.HeadSHA, ports.ReviewApprove, "LGTM")
	if err == nil || !strings.Contains(err.Error(), "Can not approve your own pull request") {
		t.Errorf("ReviewPullRequest() approving one's own pull request = %v, want GitHub's refusal", err)
	}
	if err := s.h.ReviewPullRequest(s.ctx, s.repo, pr.Number, pr.HeadSHA, "SHIP_IT", ""); err == nil ||
		!strings.Contains(err.Error(), `no such review event "SHIP_IT"`) {
		t.Errorf("ReviewPullRequest() of an event the port has none of = %v", err)
	}
	if err := grace.ReviewPullRequest(s.ctx, s.repo, pr.Number, pr.HeadSHA, ports.ReviewRequestChanges, "Fix it"); err != nil {
		t.Fatal(err)
	}
	if err := s.h.ReviewPullRequest(s.ctx, s.repo, pr.Number, pr.HeadSHA, ports.ReviewComment, "On it"); err != nil {
		t.Fatal(err)
	}
	s.gh.AddPendingReview("acme", "shop", pr.Number, "linus", "A draft")
	moved, err := s.h.PushBranch(s.ctx, s.repo, s.head, "task/review", scaffold.Tree{"src/a.txt": {Data: []byte("A\n")}},
		nil, "Fix it", shopAuthor)
	if err != nil {
		t.Fatal(err)
	}
	if err := grace.ReviewPullRequest(s.ctx, s.repo, pr.Number, moved, ports.ReviewApprove, "Good"); err != nil {
		t.Fatal(err)
	}
	want := []ports.PullReview{
		{User: "grace", State: ports.ReviewRequestChanges, Commit: pr.HeadSHA, Body: "Fix it", Stale: true},
		{User: "ada", State: ports.ReviewComment, Commit: pr.HeadSHA, Body: "On it", Stale: true},
		{User: "grace", State: ports.ReviewApprove, Commit: moved, Body: "Good"},
	}
	got, err := s.h.PullRequestReviews(s.ctx, s.repo, pr.Number)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("PullRequestReviews() = %+v, %v; want %+v", got, err, want)
	}
	ids := s.gh.Reviews("acme", "shop", pr.Number)
	s.gh.DismissReview("acme", "shop", pr.Number, ids[len(ids)-1])
	want[2].State, want[2].Dismissed = ports.ReviewComment, true
	if got, err := s.h.PullRequestReviews(s.ctx, s.repo, pr.Number); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("PullRequestReviews() after a dismissal = %+v, %v; want %+v", got, err, want)
	}
}

func (s shop) comment(t *testing.T) {
	pr := s.open(t, "task/comment", scaffold.Tree{"src/c.txt": {Data: []byte("c\n")}})
	if err := s.h.CommentPullRequest(s.ctx, s.repo, pr.Number, "Checks failed"); err != nil {
		t.Fatal(err)
	}
	if got := s.gh.Comments("acme", "shop", pr.Number); !slices.Equal(got, []string{"Checks failed"}) {
		t.Errorf("comments %q", got)
	}
}

func (s shop) sizeBehindCommits(t *testing.T) {
	pr := s.open(t, "task/size", scaffold.Tree{"src/d.txt": {Data: []byte("one\ntwo\n")},
		shopReadme: {Data: []byte("hi\n")}})
	if pr.Additions != 3 || pr.Deletions != 1 || pr.ChangedFiles != 2 || pr.Behind || pr.State != "open" ||
		!strings.HasSuffix(pr.URL, fmt.Sprintf("/acme/shop/pull/%d", pr.Number)) {
		t.Errorf("OpenPullRequest() = %+v, want +3 -1 in 2 files, not behind", pr)
	}
	main, _ := s.h.BranchCommit(s.ctx, s.repo, ports.DefaultBranch)
	if _, err := s.h.PushDefaultBranch(s.ctx, s.repo, main, scaffold.Tree{"other.txt": {Data: []byte("o\n")}}, nil,
		"Move main", shopAuthor); err != nil {
		t.Fatal(err)
	}
	if got, err := s.h.GetPullRequest(s.ctx, s.repo, pr.Number); err != nil || !got.Behind || !got.Mergeable {
		t.Errorf("GetPullRequest() once main moved = %+v, %v; want it behind and mergeable", got, err)
	}
	for range 2 {
		// The second finds nothing new on the base, which is no error.
		if err := s.h.UpdatePullRequest(s.ctx, s.repo, pr.Number); err != nil {
			t.Fatalf("UpdatePullRequest() = %v", err)
		}
	}
	updated, err := s.h.GetPullRequest(s.ctx, s.repo, pr.Number)
	if err != nil || updated.Behind || updated.HeadSHA == pr.HeadSHA {
		t.Errorf("GetPullRequest() after the update = %+v, %v; want a new head that is not behind", updated, err)
	}
	before, err := s.h.PullRequestCommits(s.ctx, s.repo, pr.Number)
	if err != nil || len(before) != 2 || before[0].SHA != pr.HeadSHA || before[0].Parents != 1 ||
		before[0].AuthorEmail != shopAuthor.Email || before[1].SHA != updated.HeadSHA || before[1].Parents != 2 {
		t.Errorf("PullRequestCommits() = %+v, %v; want the change, then the merge of main", before, err)
	}
	if err := s.h.MergePullRequest(s.ctx, s.repo, pr.Number, updated.HeadSHA, "Merge", ""); err != nil {
		t.Fatal(err)
	}
	if has, _ := s.h.HasBranch(s.ctx, s.repo, "task/size"); has {
		t.Error("the merged branch is still there")
	}
	if after, err := s.h.PullRequestCommits(s.ctx, s.repo, pr.Number); err != nil || !reflect.DeepEqual(after, before) {
		t.Errorf("PullRequestCommits() after the merge = %+v, %v; want %+v", after, err, before)
	}
}

func (s shop) closed(t *testing.T) {
	pr := s.open(t, "task/close", scaffold.Tree{"src/e.txt": {Data: []byte("e\n")}})
	if err := s.h.ClosePullRequest(s.ctx, s.repo, pr.Number); err != nil {
		t.Fatal(err)
	}
	if got, err := s.h.GetPullRequest(s.ctx, s.repo, pr.Number); err != nil || got.State != "closed" || got.Merged ||
		got.MergeCommit != "" {
		t.Errorf("GetPullRequest() of a closed pull request = %+v, %v", got, err)
	}
	again, err := s.h.OpenPullRequest(s.ctx, s.repo, "task/close", ports.DefaultBranch, "Again", "")
	if err != nil || again.Number == pr.Number {
		t.Errorf("OpenPullRequest() after a close = %+v, %v; want a new pull request", again, err)
	}
}

func (s shop) emptyBranch(t *testing.T) {
	main, _ := s.h.BranchCommit(s.ctx, s.repo, ports.DefaultBranch)
	if err := s.h.CreateBranch(s.ctx, s.repo, "task/empty", main); err != nil {
		t.Fatal(err)
	}
	_, err := s.h.OpenPullRequest(s.ctx, s.repo, "task/empty", ports.DefaultBranch, "Nothing", "")
	if err == nil || !strings.Contains(err.Error(), "No commits between main and task/empty") {
		t.Errorf("OpenPullRequest() of a branch with no commit of its own = %v", err)
	}
}

func (s shop) missingPull(t *testing.T) {
	for what, err := range map[string]error{
		"merge":  s.h.MergePullRequest(s.ctx, s.repo, 999, s.head, "Merge", ""),
		"update": s.h.UpdatePullRequest(s.ctx, s.repo, 999),
		"close":  s.h.ClosePullRequest(s.ctx, s.repo, 999),
	} {
		if err == nil {
			t.Errorf("%s of a missing pull request went through", what)
		}
	}
	if _, err := s.h.PullRequestFiles(s.ctx, s.repo, 999); !errors.Is(err, ports.ErrPullRequestNotFound) {
		t.Errorf("PullRequestFiles() of a missing pull request = %v, want ErrPullRequestNotFound", err)
	}
	if _, err := s.h.PullRequestReviews(s.ctx, s.repo, 999); !errors.Is(err, ports.ErrPullRequestNotFound) {
		t.Errorf("PullRequestReviews() of a missing pull request = %v, want ErrPullRequestNotFound", err)
	}
}

func (s shop) checkRunStates(t *testing.T) {
	for conclusion, want := range map[string]string{"success": ports.CheckSuccess, "neutral": ports.CheckSuccess,
		"skipped": ports.CheckSuccess, "failure": ports.CheckFailure, "cancelled": ports.CheckFailure,
		"timed_out": ports.CheckFailure, "action_required": ports.CheckFailure, "stale": ports.CheckFailure} {
		if got := (ghCheckRun{Status: "completed", Conclusion: &conclusion}).state(); got != want {
			t.Errorf("a run completed as %s reads %s, want %s", conclusion, got, want)
		}
	}
	for _, status := range []string{"queued", "in_progress", "waiting", "completed"} {
		if got := (ghCheckRun{Status: status}).state(); got != ports.CheckPending {
			t.Errorf("a run %s with no conclusion reads %s, want pending", status, got)
		}
	}
}

func (s shop) worstCheck(t *testing.T) {
	for _, tt := range []struct {
		name     string
		statuses map[string]string
		runs     map[string][2]string
		want     string
		page     string
	}{
		{"all passed", map[string]string{"theos/review": ports.CheckSuccess},
			map[string][2]string{checkUnit: {"completed", "success"}}, ports.CheckSuccess, "/runs/" + checkUnit},
		{"one pending", map[string]string{"theos/review": ports.CheckPending},
			map[string][2]string{checkUnit: {"completed", "success"}}, ports.CheckPending, "/status/theos/review"},
		{"an error beats a pending run", map[string]string{"theos/review": ports.CheckError},
			map[string][2]string{checkUnit: {"queued", ""}}, ports.CheckError, "/status/theos/review"},
		{"a failed run beats an error", map[string]string{"theos/review": ports.CheckError},
			map[string][2]string{checkUnit: {"completed", "failure"}}, ports.CheckFailure, "/runs/" + checkUnit},
	} {
		sha := s.commit(t, strings.ReplaceAll(tt.name, " ", "-"))
		contexts := map[string]string{}
		for check, state := range tt.statuses {
			s.setStatus(t, sha, check, state, s.gh.URL()+"/status/"+check)
			contexts[check] = state
		}
		for run, at := range tt.runs {
			s.gh.AddCheckRun("acme", "shop", sha, run, at[0], at[1])
			contexts[run] = (ghCheckRun{Status: at[0], Conclusion: &at[1]}).state()
			if at[1] == "" {
				contexts[run] = ports.CheckPending
			}
		}
		got, err := s.h.CombinedStatus(s.ctx, s.repo, sha)
		if err != nil || got.Commit != sha || got.State != tt.want || !maps.Equal(got.Contexts, contexts) ||
			!strings.HasSuffix(got.URL, tt.page) {
			t.Errorf("%s: CombinedStatus() = %+v, %v; want %s with %v and the page %s", tt.name, got, err, tt.want,
				contexts, tt.page)
		}
	}
}

func (s shop) latestCheck(t *testing.T) {
	sha := s.commit(t, "latest")
	s.setStatus(t, sha, "theos/review", ports.CheckFailure, "")
	s.setStatus(t, sha, "theos/review", ports.CheckSuccess, "")
	s.gh.AddCheckRun("acme", "shop", sha, checkUnit, "completed", "failure")
	s.gh.AddCheckRun("acme", "shop", sha, checkUnit, "completed", "success")
	for _, ref := range []string{sha, ports.DefaultBranch} {
		got, err := s.h.CombinedStatus(s.ctx, s.repo, ref)
		want := map[string]string{"theos/review": ports.CheckSuccess, checkUnit: ports.CheckSuccess}
		if err != nil || got.Commit != sha || got.State != ports.CheckSuccess || !maps.Equal(got.Contexts, want) {
			t.Errorf("CombinedStatus(%s) = %+v, %v; want success with %v", ref, got, err, want)
		}
	}
	got, err := s.h.CommitStatuses(s.ctx, s.repo, sha)
	want := []ports.CommitStatus{{Context: "theos/review", State: ports.CheckSuccess},
		{Context: checkUnit, State: ports.CheckSuccess, TargetURL: s.gh.URL() + "/acme/shop/runs/" + checkUnit}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("CommitStatuses() = %+v, %v; want %+v", got, err, want)
	}
}

func (s shop) longDescription(t *testing.T) {
	sha := s.commit(t, "long")
	long := strings.Repeat("é", 200)
	if err := s.h.SetCommitStatus(s.ctx, s.repo, sha, ports.CommitStatus{Context: "theos/review",
		State: ports.CheckPending, Description: long, TargetURL: "https://example.com/1"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.h.CommitStatuses(s.ctx, s.repo, sha)
	if err != nil || len(got) != 1 || got[0].TargetURL != "https://example.com/1" ||
		got[0].Description != strings.Repeat("é", 139)+"…" {
		t.Errorf("CommitStatuses() = %+v, %v; want the description cut to 140 characters", got, err)
	}
}

func (s shop) manyChecks(t *testing.T) {
	sha := s.commit(t, "many")
	want := map[string]string{}
	for n := range 130 {
		s.setStatus(t, sha, fmt.Sprintf("status/%d", n), ports.CheckSuccess, "")
		s.gh.AddCheckRun("acme", "shop", sha, fmt.Sprintf("run/%d", n), "completed", "success")
		want[fmt.Sprintf("status/%d", n)], want[fmt.Sprintf("run/%d", n)] = ports.CheckSuccess, ports.CheckSuccess
	}
	got, err := s.h.CombinedStatus(s.ctx, s.repo, sha)
	if err != nil || got.State != ports.CheckSuccess || !maps.Equal(got.Contexts, want) {
		t.Errorf("CombinedStatus() reads %d checks, %v; want all %d", len(got.Contexts), err, len(want))
	}
	if all, err := s.h.CommitStatuses(s.ctx, s.repo, sha); err != nil || len(all) != len(want) {
		t.Errorf("CommitStatuses() reads %d checks, %v; want all %d", len(all), err, len(want))
	}
}

func (s shop) missingCommit(t *testing.T) {
	missing := strings.Repeat("0", 40)
	if _, err := s.h.CombinedStatus(s.ctx, s.repo, missing); err == nil {
		t.Error("CombinedStatus() of a missing commit went through")
	}
	if err := s.h.SetCommitStatus(s.ctx, s.repo, missing, ports.CommitStatus{Context: "c",
		State: ports.CheckSuccess}); err == nil {
		t.Error("SetCommitStatus() on a missing commit went through")
	}
}
