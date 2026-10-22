package model

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestNextPublishStep(t *testing.T) {
	const marker = "<!-- c4factory: cel-lens/extension v0.2.0 -->"
	const url = "https://github.com/JairJosafath/cel-lens/releases/tag/v0.2.0"
	// name is the release's name: the app, then the Crew's tag, no prefix.
	const name = "CEL Lens for VS Code v0.2.0"
	vsix := ReleaseAsset{ID: 1, Name: "cel-lens-0.2.0.vsix", Size: 4096}
	sums := ReleaseAsset{ID: 2, Name: "SHA256SUMS", Size: 120}
	gitea := &SourceRelease{Body: "- Lens the CEL (#3)\n", Assets: []ReleaseAsset{vsix, sums}}
	draft := func(assets ...ReleaseAsset) TargetRelease {
		return TargetRelease{ID: 9, TagName: "v0.2.0", Draft: true, Body: "- Lens the CEL (#3)\n\n" + marker + "\n",
			Assets: assets}
	}
	remote := &PublishTarget{Repository: "JairJosafath/cel-lens", ID: 7}
	onGitHub := func(releases ...TargetRelease) *TargetReleases { return &TargetReleases{Releases: releases} }
	// ready is a release whose runs succeeded, with its Secret, read from both
	// sides unless f says otherwise.
	ready := func(f PublishFacts) PublishFacts {
		f.Configured, f.Namespace, f.Container, f.Tag = true, "cel-lens", "extension", "v0.2.0"
		f.App, f.Latest = "CEL Lens for VS Code", true
		f.Repository, f.Secret, f.SecretFound, f.Runs = "JairJosafath/cel-lens", "cel-lens-github", true, RunSucceeded
		return f
	}
	read := func(f PublishFacts) PublishFacts {
		f = ready(f)
		if f.Gitea == nil {
			f.Gitea = gitea
		}
		if f.Remote == nil {
			f.Remote = remote
		}
		if f.GitHub == nil {
			f.GitHub = onGitHub()
		}
		return f
	}
	// readyBut is a ready release that change makes otherwise.
	readyBut := func(change func(*PublishFacts)) PublishFacts {
		f := ready(PublishFacts{})
		change(&f)
		return f
	}
	// notLatest is a release with every asset on its draft, of an app that
	// doesn't take Latest.
	notLatest := read(PublishFacts{GitHub: onGitHub(draft(
		ReleaseAsset{ID: 71, Name: sums.Name, Size: sums.Size},
		ReleaseAsset{ID: 70, Name: vsix.Name, Size: vsix.Size}))})
	notLatest.Latest = false
	failure := func(reason string) PublishFacts {
		return ready(PublishFacts{Failure: &PublishFailure{Reason: reason, Detail: "HTTP 418"}})
	}

	tests := []struct {
		name  string
		facts PublishFacts
		want  PublishStep
		// message must contain this, for steps that have one.
		message string
	}{
		{name: "a Crew that doesn't publish does nothing",
			facts: PublishFacts{Tag: "v0.2.0", Runs: RunSucceeded}, want: PublishStep{Action: PublishNone}},
		{name: "a published release is not touched again",
			facts: ready(PublishFacts{Published: true}), want: PublishStep{Action: PublishNone}},

		{name: "runs not started wait",
			facts: readyBut(func(f *PublishFacts) { f.Runs = RunWaiting }),
			want:  PublishStep{Action: PublishWait, Reason: PublishWaiting}, message: "they are not started"},
		{name: "running runs wait",
			facts: readyBut(func(f *PublishFacts) { f.Runs = RunRunning }),
			want:  PublishStep{Action: PublishWait, Reason: PublishWaiting}, message: "they are running"},
		{name: "a failed run waits for a re-run",
			facts: readyBut(func(f *PublishFacts) { f.Runs = RunFailed }),
			want:  PublishStep{Action: PublishWait, Reason: PublishWaiting}, message: "waits for a re-run"},
		{name: "runs wait before the Secret is looked at",
			facts: PublishFacts{Configured: true, Tag: "v0.2.0", Runs: RunRunning},
			want:  PublishStep{Action: PublishWait, Reason: PublishWaiting}},

		{name: "a missing Secret fails",
			facts:   readyBut(func(f *PublishFacts) { f.SecretFound = false }),
			want:    PublishStep{Action: PublishFail, Reason: PublishSecretMissing},
			message: "Secret cel-lens-github with the key token (or a GitHub App's appId, installationId, privateKey) is missing"},
		{name: "a Secret that can't be read fails with why",
			facts: readyBut(func(f *PublishFacts) {
				f.SecretFound, f.SecretError = false, `secrets "cel-lens-github" is forbidden`
			}),
			want: PublishStep{Action: PublishFail, Reason: PublishSecretUnreadable},
			message: `Secret cel-lens-github can't be read: secrets "cel-lens-github" is forbidden; ` +
				"v0.2.0 is published once it can"},
		{name: "runs wait before a Secret read error is reported",
			facts: readyBut(func(f *PublishFacts) { f.Runs, f.SecretError = RunRunning, "timeout" }),
			want:  PublishStep{Action: PublishWait, Reason: PublishWaiting}},

		{name: "GitHub refusing the token fails",
			facts: failure(PublishUnauthorized), want: PublishStep{Action: PublishFail, Reason: PublishUnauthorized},
			message: "GitHub refused the token in Secret cel-lens-github for JairJosafath/cel-lens: the token can't " +
				"write releases to JairJosafath/cel-lens. Check that JairJosafath/cel-lens is where releases should " +
				"go (set by the Crew's spec.publishReleases) and that the token is for JairJosafath/cel-lens with " +
				"Contents read and write: HTTP 418"},
		{name: "a refused token names the Brief that sets the target",
			facts: readyBut(func(f *PublishFacts) {
				f.Brief, f.Failure = "cel-lens", &PublishFailure{Reason: PublishUnauthorized}
			}),
			want:    PublishStep{Action: PublishFail, Reason: PublishUnauthorized},
			message: "(set by Brief cel-lens's spec.releases)"},
		{name: "a repository the token can't see fails",
			facts:   failure(PublishRepositoryNotFound),
			want:    PublishStep{Action: PublishFail, Reason: PublishRepositoryNotFound},
			message: "no repository JairJosafath/cel-lens the token in Secret cel-lens-github can see"},
		{name: "an empty repository fails",
			facts: failure(PublishEmptyRepository), want: PublishStep{Action: PublishFail, Reason: PublishEmptyRepository},
			message: "has no default branch to tag v0.2.0 at"},
		{name: "a tag GitHub says is taken fails",
			facts: failure(PublishTagTaken), want: PublishStep{Action: PublishFail, Reason: PublishTagTaken},
			message: "the factory didn't make"},
		{name: "any other GitHub failure is a GitHub error",
			facts: failure(PublishGitHubError), want: PublishStep{Action: PublishFail, Reason: PublishGitHubError},
			message: "GitHub failed publishing v0.2.0 to JairJosafath/cel-lens: HTTP 418"},
		{name: "an unknown failure is a GitHub error",
			facts: failure("Teapot"), want: PublishStep{Action: PublishFail, Reason: PublishGitHubError}},

		{name: "the Gitea release is read first",
			facts: ready(PublishFacts{}), want: PublishStep{Action: PublishReadGitea}},
		{name: "a missing Gitea release fails",
			facts: ready(PublishFacts{Gitea: &SourceRelease{Missing: true}}),
			want:  PublishStep{Action: PublishFail, Reason: PublishNoReleaseFiles}, message: "Gitea has no release v0.2.0"},
		{name: "a Gitea release without files fails",
			facts: ready(PublishFacts{Gitea: &SourceRelease{Body: "notes"}}),
			want:  PublishStep{Action: PublishFail, Reason: PublishNoReleaseFiles}, message: "has no files"},
		{name: "the repository is read next",
			facts: ready(PublishFacts{Gitea: gitea}), want: PublishStep{Action: PublishReadRepository}},
		{name: "GitHub's releases are read next",
			facts: ready(PublishFacts{Gitea: gitea, Remote: remote}), want: PublishStep{Action: PublishReadGitHub}},
		{name: "the repository GitHub answers by another name was renamed",
			facts: ready(PublishFacts{Gitea: gitea, Remote: &PublishTarget{Repository: "JairJosafath/private-cel-lens",
				ID: 7}}),
			want: PublishStep{Action: PublishFail, Reason: PublishRepositoryRenamed},
			message: "JairJosafath/cel-lens was renamed to JairJosafath/private-cel-lens; point the Crew's " +
				"spec.publishReleases at JairJosafath/private-cel-lens, or at another repository"},
		{name: "a renamed repository names the Brief that sets the target",
			facts: ready(PublishFacts{Brief: "cel-lens", Gitea: gitea,
				Remote: &PublishTarget{Repository: "JairJosafath/private-cel-lens", ID: 7}}),
			want:    PublishStep{Action: PublishFail, Reason: PublishRepositoryRenamed},
			message: "point Brief cel-lens's spec.releases at JairJosafath/private-cel-lens"},
		{name: "GitHub's case of the name is the same repository",
			facts: ready(PublishFacts{Gitea: gitea, Remote: &PublishTarget{Repository: "jairjosafath/CEL-lens", ID: 7}}),
			want:  PublishStep{Action: PublishReadGitHub}},
		{name: "the repository the Crew published to before goes on",
			facts: ready(PublishFacts{Gitea: gitea, Remote: remote, Recorded: remote}),
			want:  PublishStep{Action: PublishReadGitHub}},
		{name: "another repository under the name the Crew published to was replaced",
			facts: ready(PublishFacts{Gitea: gitea, Recorded: remote,
				Remote: &PublishTarget{Repository: "JairJosafath/cel-lens", ID: 8}}),
			want: PublishStep{Action: PublishFail, Reason: PublishRepositoryReplaced},
			message: "JairJosafath/cel-lens is not the repository this Crew published to before (GitHub id 7, now 8): " +
				"it was deleted or renamed and a new one took its name. Nothing is published to it until you point " +
				"the Crew's spec.publishReleases at the repository wanted, or accept this one with the annotation " +
				`c4.factory.io/accept-repository: "8" on the Crew`},
		{name: "a replaced repository is refused before its releases are read",
			facts: read(PublishFacts{Recorded: remote, Remote: &PublishTarget{Repository: "JairJosafath/cel-lens", ID: 8},
				GitHub: onGitHub(draft(vsix, sums))}),
			want: PublishStep{Action: PublishFail, Reason: PublishRepositoryReplaced}},
		{name: "an accepted replacement goes on",
			facts: ready(PublishFacts{Gitea: gitea, Recorded: remote, Accept: "8",
				Remote: &PublishTarget{Repository: "JairJosafath/cel-lens", ID: 8}}),
			want: PublishStep{Action: PublishReadGitHub}},
		{name: "accepting another id leaves the replacement refused",
			facts: ready(PublishFacts{Gitea: gitea, Recorded: remote, Accept: "9",
				Remote: &PublishTarget{Repository: "JairJosafath/cel-lens", ID: 8}}),
			want: PublishStep{Action: PublishFail, Reason: PublishRepositoryReplaced}},
		{name: "a record of another repository doesn't hold the new target",
			facts: ready(PublishFacts{Gitea: gitea, Recorded: &PublishTarget{Repository: "JairJosafath/old", ID: 3},
				Remote: remote}),
			want: PublishStep{Action: PublishReadGitHub}},

		{name: "a release of the tag without the marker is taken",
			facts:   read(PublishFacts{GitHub: onGitHub(TargetRelease{ID: 4, TagName: "v0.2.0", Body: "mine"})}),
			want:    PublishStep{Action: PublishFail, Reason: PublishTagTaken},
			message: "JairJosafath/cel-lens already has a release or tag v0.2.0"},
		{name: "a bare tag of that name is taken",
			facts: read(PublishFacts{GitHub: &TargetReleases{TagExists: true}}),
			want:  PublishStep{Action: PublishFail, Reason: PublishTagTaken}},
		{name: "a marked release of another tag doesn't count",
			facts: read(PublishFacts{GitHub: onGitHub(TargetRelease{ID: 4, TagName: "v0.1.0",
				Body: "<!-- c4factory: cel-lens/extension v0.1.0 -->"})}),
			want: PublishStep{Action: PublishCreateDraft, Tag: "v0.2.0", Name: name, Body: "- Lens the CEL (#3)\n\n" + marker + "\n"}},
		{name: "another Container's marked release of the tag is taken",
			facts: read(PublishFacts{GitHub: onGitHub(TargetRelease{ID: 4, TagName: "v0.2.0",
				Body: "<!-- c4factory: cel-lens/cli v0.2.0 -->"})}),
			want: PublishStep{Action: PublishFail, Reason: PublishTagTaken}},

		{name: "notes nobody wrote need the merged issues",
			facts: read(PublishFacts{Gitea: &SourceRelease{Body: " \n", Assets: []ReleaseAsset{vsix}}}),
			want:  PublishStep{Action: PublishReadIssues}},
		{name: "notes nobody wrote are written on Gitea first",
			facts: read(PublishFacts{Gitea: &SourceRelease{Assets: []ReleaseAsset{vsix}},
				Issues: []ReleaseIssue{{3, "Lens the CEL"}, {5, "Show errors"}}}),
			want: PublishStep{Action: PublishWriteNotes, Tag: "v0.2.0",
				Body: "- Lens the CEL (#3)\n- Show errors (#5)\n"}},
		{name: "a release with no notes and no merged issue gets the marker alone",
			facts: read(PublishFacts{Gitea: &SourceRelease{Assets: []ReleaseAsset{vsix}}, Issues: []ReleaseIssue{}}),
			want:  PublishStep{Action: PublishCreateDraft, Tag: "v0.2.0", Name: name, Body: marker + "\n"}},
		{name: "a release with notes gets a marked draft",
			facts: read(PublishFacts{}),
			want:  PublishStep{Action: PublishCreateDraft, Tag: "v0.2.0", Name: name, Body: "- Lens the CEL (#3)\n\n" + marker + "\n"}},
		{name: "the prefix goes before the tag, in the marker too",
			facts: read(PublishFacts{TagPrefix: "cli-"}),
			want: PublishStep{Action: PublishCreateDraft, Tag: "cli-v0.2.0", Name: name,
				Body: "- Lens the CEL (#3)\n\n<!-- c4factory: cel-lens/extension cli-v0.2.0 -->\n"}},

		{name: "retry: a draft without assets gets the first",
			facts: read(PublishFacts{GitHub: onGitHub(draft())}),
			want:  PublishStep{Action: PublishUploadAsset, Release: 9, Asset: vsix}},
		{name: "retry: a draft with some assets gets the next",
			facts: read(PublishFacts{GitHub: onGitHub(draft(ReleaseAsset{ID: 70, Name: vsix.Name, Size: vsix.Size}))}),
			want:  PublishStep{Action: PublishUploadAsset, Release: 9, Asset: sums}},
		{name: "retry: an asset of the wrong size is replaced",
			facts: read(PublishFacts{GitHub: onGitHub(draft(ReleaseAsset{ID: 70, Name: vsix.Name, Size: 1000}))}),
			want: PublishStep{Action: PublishUploadAsset, Release: 9, Asset: vsix,
				Replace: &ReleaseAsset{ID: 70, Name: vsix.Name, Size: 1000}}},
		{name: "retry: the draft is found among other releases",
			facts: read(PublishFacts{GitHub: onGitHub(TargetRelease{ID: 4, TagName: "v0.1.0", Body: "old"},
				draft(ReleaseAsset{ID: 70, Name: vsix.Name, Size: vsix.Size}))}),
			want: PublishStep{Action: PublishUploadAsset, Release: 9, Asset: sums}},
		{name: "retry: a draft with every asset is published",
			facts: read(PublishFacts{GitHub: onGitHub(draft(
				ReleaseAsset{ID: 71, Name: sums.Name, Size: sums.Size},
				ReleaseAsset{ID: 70, Name: vsix.Name, Size: vsix.Size},
				ReleaseAsset{ID: 72, Name: "extra.txt", Size: 1}))}),
			want: PublishStep{Action: PublishDraft, Release: 9, Latest: true}},
		{name: "retry: a draft with notes nobody wrote doesn't write them again",
			facts: read(PublishFacts{Gitea: &SourceRelease{Assets: []ReleaseAsset{vsix}},
				GitHub: onGitHub(draft(ReleaseAsset{ID: 70, Name: vsix.Name, Size: vsix.Size}))}),
			want: PublishStep{Action: PublishDraft, Release: 9, Latest: true}},
		{name: "a release of an app that isn't Latest is published as not Latest",
			facts: notLatest, want: PublishStep{Action: PublishDraft, Release: 9}},
		{name: "a published marked release is done",
			facts:   read(PublishFacts{GitHub: onGitHub(TargetRelease{ID: 9, TagName: "v0.2.0", Body: marker, URL: url})}),
			want:    PublishStep{Action: PublishDone, Reason: PublishPublished, URL: url},
			message: "v0.2.0 is published at " + url},
		{name: "a published marked release wins over a stray draft",
			facts: read(PublishFacts{GitHub: onGitHub(draft(),
				TargetRelease{ID: 10, TagName: "v0.2.0", Body: marker, URL: url})}),
			want: PublishStep{Action: PublishDone, Reason: PublishPublished, URL: url}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NextPublishStep(tt.facts)
			if !strings.Contains(got.Message, tt.message) {
				t.Errorf("message = %q, want it to contain %q", got.Message, tt.message)
			}
			got.Message = ""
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NextPublishStep() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReleaseName(t *testing.T) {
	for app, want := range map[string]string{"CEL Lens": "CEL Lens v0.2.0", "": "v0.2.0"} {
		if got := ReleaseName(app, "v0.2.0"); got != want {
			t.Errorf("ReleaseName(%q, v0.2.0) = %q, want %q", app, got, want)
		}
	}
}

// TestNextPublishStepRuns takes a release from nothing on GitHub to published,
// taking each step on a fake of both sides, and again from a failure halfway.
func TestNextPublishStepRuns(t *testing.T) {
	assets := []ReleaseAsset{{1, "a.vsix", 10}, {2, "cli-linux", 20}, {3, "SHA256SUMS", 30}}
	for _, failAt := range []PublishAction{"", PublishCreateDraft, PublishUploadAsset, PublishDraft} {
		t.Run("fail at "+string(failAt), func(t *testing.T) {
			giteaBody := ""
			gh := &TargetReleases{}
			failed, replaced := false, false
			var actions []PublishAction
			for range 20 {
				f := PublishFacts{Configured: true, Namespace: "ns", Container: "app", Tag: "v1.0.0",
					Repository: "o/r", Secret: "s", SecretFound: true, Runs: RunSucceeded}
				for {
					step := NextPublishStep(f)
					switch step.Action {
					case PublishReadGitea:
						f.Gitea = &SourceRelease{Body: giteaBody, Assets: assets}
						continue
					case PublishReadIssues:
						f.Issues = []ReleaseIssue{{1, "First"}}
						continue
					case PublishReadRepository:
						f.Remote = &PublishTarget{Repository: f.Repository, ID: 7}
						continue
					case PublishReadGitHub:
						f.GitHub = gh
						continue
					}
					actions = append(actions, step.Action)
					if step.Action == failAt && !failed {
						// The call half-happened: an upload left a short asset.
						failed = true
						if step.Action == PublishUploadAsset {
							r := &gh.Releases[0]
							r.Assets = append(r.Assets, ReleaseAsset{ID: 99, Name: step.Asset.Name, Size: 1})
						}
						f.Failure = &PublishFailure{Reason: PublishGitHubError}
						if got := NextPublishStep(f); got.Reason != PublishGitHubError {
							t.Fatalf("after a failed call: %+v", got)
						}
						break
					}
					switch step.Action {
					case PublishWriteNotes:
						giteaBody = step.Body
					case PublishCreateDraft:
						gh.Releases = append(gh.Releases, TargetRelease{ID: 7, TagName: step.Tag, Draft: true, Body: step.Body})
					case PublishUploadAsset:
						r := &gh.Releases[0]
						if step.Replace != nil {
							replaced = true
							r.Assets = slices.DeleteFunc(r.Assets, func(a ReleaseAsset) bool { return a.ID == step.Replace.ID })
						}
						r.Assets = append(r.Assets, ReleaseAsset{ID: 100 + step.Asset.ID, Name: step.Asset.Name,
							Size: step.Asset.Size})
					case PublishDraft:
						gh.Releases[0].Draft, gh.Releases[0].URL = false, "https://github.com/o/r/releases/tag/v1.0.0"
					case PublishDone:
						r := gh.Releases[0]
						if len(r.Assets) != 3 || r.Body != "- First (#1)\n\n"+PublishMarker("ns", "app", "v1.0.0")+"\n" {
							t.Fatalf("published %+v", r)
						}
						if replaced != (failAt == PublishUploadAsset) {
							t.Fatalf("replaced an asset: %v, want it only after a failed upload", replaced)
						}
						for _, a := range r.Assets {
							if a.Size == 1 {
								t.Fatalf("published a short asset: %+v", r.Assets)
							}
						}
						return
					default:
						t.Fatalf("step %+v", step)
					}
					break
				}
			}
			t.Fatalf("not published after 20 polls: %v", actions)
		})
	}
}

func TestReleaseNotes(t *testing.T) {
	got := ReleaseNotes([]ReleaseIssue{{3, "Lens the CEL"}, {12, "Show `errors` inline"}})
	if want := "- Lens the CEL (#3)\n- Show `errors` inline (#12)\n"; got != want {
		t.Errorf("ReleaseNotes() = %q, want %q", got, want)
	}
	if got := ReleaseNotes(nil); got != "" {
		t.Errorf("ReleaseNotes(nil) = %q", got)
	}
}

func TestMergedReleaseIssues(t *testing.T) {
	closed := []ReleaseIssue{{3, "Lens the CEL"}, {7, "Check the cost of every expression"}, {12, "Show errors"}}
	got := MergedReleaseIssues(closed, []int{12, 3, 40})
	want := []ReleaseIssue{{3, "Lens the CEL"}, {12, "Show errors"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MergedReleaseIssues() = %v, want %v: #7 closed without a merge", got, want)
	}
	if got := MergedReleaseIssues(closed, nil); len(got) != 0 {
		t.Errorf("MergedReleaseIssues(no merges) = %v", got)
	}
}

func TestPublishMarkerAndTag(t *testing.T) {
	if got := GitHubTag("cli-", "v0.2.0"); got != "cli-v0.2.0" {
		t.Errorf("GitHubTag = %q", got)
	}
	if got := GitHubTag("", "v0.2.0"); got != "v0.2.0" {
		t.Errorf("GitHubTag = %q", got)
	}
	if got := PublishMarker("cel-lens", "extension", "v0.2.0"); got != "<!-- c4factory: cel-lens/extension v0.2.0 -->" {
		t.Errorf("PublishMarker = %q", got)
	}
}

func TestNextPublishTarget(t *testing.T) {
	const repo = "JairJosafath/cel-lens"
	before := &PublishTarget{Repository: repo, ID: 7}
	replacement := &PublishTarget{Repository: repo, ID: 8}
	done := PublishStep{Action: PublishDone}
	failed := PublishStep{Action: PublishFail}
	tests := []struct {
		name         string
		facts        PublishFacts
		step         PublishStep
		want         *PublishTarget
		wantAccepted bool
	}{
		{name: "nothing is recorded before a publish succeeds",
			facts: PublishFacts{Repository: repo, Remote: before}, step: PublishStep{Action: PublishCreateDraft}},
		{name: "the first publish that succeeds records the repository",
			facts: PublishFacts{Repository: repo, Remote: before}, step: done, want: before},
		{name: "a publish not read this pass records nothing",
			facts: PublishFacts{Repository: repo}, step: done},
		{name: "a renamed repository is not recorded",
			facts: PublishFacts{Repository: repo, Remote: &PublishTarget{Repository: "JairJosafath/private-cel-lens", ID: 7}},
			step:  done},
		{name: "the record stays",
			facts: PublishFacts{Repository: repo, Recorded: before, Remote: before}, step: done, want: before},
		{name: "a replaced repository leaves the record",
			facts: PublishFacts{Repository: repo, Recorded: before, Remote: replacement}, step: failed, want: before},
		{name: "an accepted id is recorded and the annotation taken",
			facts: PublishFacts{Repository: repo, Recorded: before, Remote: replacement, Accept: "8"},
			step:  PublishStep{Action: PublishCreateDraft}, want: replacement, wantAccepted: true},
		{name: "another id than GitHub's isn't accepted",
			facts: PublishFacts{Repository: repo, Recorded: before, Remote: replacement, Accept: "9"},
			step:  failed, want: before},
		{name: "an accept read before the repository waits",
			facts: PublishFacts{Repository: repo, Recorded: before, Accept: "8"}, step: failed, want: before},
		{name: "another target drops the record",
			facts: PublishFacts{Repository: "JairJosafath/elsewhere", Recorded: before}, step: failed},
		{name: "another target records its own at its first publish",
			facts: PublishFacts{Repository: "JairJosafath/elsewhere", Recorded: before,
				Remote: &PublishTarget{Repository: "JairJosafath/elsewhere", ID: 9}},
			step: done, want: &PublishTarget{Repository: "JairJosafath/elsewhere", ID: 9}},
		{name: "a target only in another case keeps the record",
			facts: PublishFacts{Repository: "jairjosafath/CEL-lens", Recorded: before}, step: failed, want: before},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, accepted := NextPublishTarget(tt.facts, tt.step)
			if !reflect.DeepEqual(got, tt.want) || accepted != tt.wantAccepted {
				t.Errorf("NextPublishTarget() = %+v, %v; want %+v, %v", got, accepted, tt.want, tt.wantAccepted)
			}
		})
	}
}

func TestIsImageArchive(t *testing.T) {
	for name, want := range map[string]bool{
		"orders-api-v1.0.0.oci.tar": true,
		"orders-api-v1.0.0.tar":     false,
		"orders-api.oci.tar.gz":     false,
		"SHA256SUMS":                false,
	} {
		if got := IsImageArchive(name); got != want {
			t.Errorf("IsImageArchive(%q) = %v, want %v", name, got, want)
		}
	}
}
