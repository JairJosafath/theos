package model

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// PublishFacts is everything the next step of publishing a Crew's last release
// to its GitHub repository depends on: the release and its state from status,
// and what the operator read so far in this pass. The reads start out unknown;
// a step that needs one asks for it.
type PublishFacts struct {
	// Configured says the Crew sets publishReleases.
	Configured bool
	// Namespace and Container name the release's Container; Tag is the Crew's
	// tag, and TagPrefix what goes before it on GitHub. App is the app's name
	// a person reads, the Container's display name or else its name.
	Namespace, Container, Tag, TagPrefix, App string
	// Latest says the release becomes the repository's Latest when published.
	Latest bool
	// Repository is the GitHub repository, owner/name. Brief names the Brief
	// whose spec.releases sets it, "" when the Crew's own
	// spec.publishReleases does.
	Repository, Brief string
	// Recorded is the repository the Crew published to before
	// (status.publishTarget), nil when none. Accept is the GitHub id a person
	// accepts in its place (c4.factory.io/accept-repository), or "".
	Recorded *PublishTarget
	Accept   string
	// Remote is the repository GitHub answers for Repository, nil until read.
	Remote *PublishTarget
	// Published says status already records this release as published: it is
	// not touched again.
	Published bool
	// Runs is the result of the release's runs (ReleaseRunResult).
	Runs string
	// Secret names the token's Secret, and SecretFound says it holds the key
	// token. SecretError is why the Secret couldn't be read, other than it not
	// being there (Forbidden, a timeout), or "".
	Secret      string
	SecretFound bool
	SecretError string
	// Failure is the GitHub call that failed in this pass, or nil.
	Failure *PublishFailure
	// Gitea is the Gitea release of Tag, nil until read.
	Gitea *SourceRelease
	// Issues are the issues merged in the release, nil until read; only read
	// when the operator writes the notes.
	Issues []ReleaseIssue
	// GitHub is what the GitHub repository holds, nil until read.
	GitHub *TargetReleases

	// Source is the folder the release's source goes to, nil when the Crew
	// publishes no source. Overlaps names the other Crews whose source
	// folders in the repository overlap it (SourceOverlaps).
	Source   *PublishSource
	Overlaps []string
	// SourceFiles is the release's source, read from Gitea at the tag, nil
	// until read.
	SourceFiles *ReleaseSource
	// GitHubSource is what the repository holds of the folder, nil until read.
	GitHubSource *GitHubSource
}

// PublishFailure is a GitHub call that failed, with its reason (one of
// PublishUnauthorized, PublishRepositoryNotFound, PublishEmptyRepository,
// PublishTagTaken, PublishSourceTooLarge, PublishBranchProtected,
// PublishBranchMoved, PublishGitHubError) and what GitHub said.
type PublishFailure struct {
	Reason, Detail string
}

// PublishTarget is a GitHub repository by name, owner/name, and GitHub's id,
// which a rename keeps and a new repository under the old name doesn't.
type PublishTarget struct {
	Repository string
	ID         int64
}

// SourceRelease is a release in Gitea. Missing says there is none for the tag.
type SourceRelease struct {
	Missing bool
	Body    string
	Assets  []ReleaseAsset
}

// ReleaseAsset is a file attached to a release.
type ReleaseAsset struct {
	ID   int64
	Name string
	Size int64
}

// ReleaseIssue is an issue merged in a release.
type ReleaseIssue struct {
	Number int
	Title  string
}

// TargetReleases is what a GitHub repository holds: its releases, drafts
// among them, and whether a tag exists.
type TargetReleases struct {
	Releases []TargetRelease
	// TagExists says the GitHub tag exists.
	TagExists bool
}

// TargetRelease is a release on GitHub.
type TargetRelease struct {
	ID      int64
	TagName string
	// Target is the draft's target_commitish: a branch, or the commit its tag
	// goes on.
	Target string
	Draft  bool
	Body   string
	URL    string
	Assets []ReleaseAsset
}

// PublishAction is what the operator does next to publish a release.
type PublishAction string

// Publish actions.
const (
	// PublishNone does nothing: the Crew doesn't publish its releases.
	PublishNone PublishAction = "None"
	// PublishReadGitea reads the Gitea release of the tag into Gitea.
	PublishReadGitea PublishAction = "ReadGitea"
	// PublishReadIssues reads the issues merged in the release into Issues.
	PublishReadIssues PublishAction = "ReadIssues"
	// PublishReadRepository reads the GitHub repository into Remote.
	PublishReadRepository PublishAction = "ReadRepository"
	// PublishReadGitHub reads the GitHub repository's releases into GitHub.
	PublishReadGitHub PublishAction = "ReadGitHub"
	// PublishWait waits for the release's runs to succeed.
	PublishWait PublishAction = "Wait"
	// PublishWriteNotes sets Body on the Gitea release, so both releases read
	// the same.
	PublishWriteNotes PublishAction = "WriteNotes"
	// PublishReadSource reads the source at the tag from Gitea into
	// SourceFiles (NewSource).
	PublishReadSource PublishAction = "ReadSource"
	// PublishReadGitHubSource reads what the repository holds of the source
	// folder into GitHubSource.
	PublishReadGitHubSource PublishAction = "ReadGitHubSource"
	// PublishSourceCommit builds SourceCommit and fast-forwards the default
	// branch to it.
	PublishSourceCommit PublishAction = "SourceCommit"
	// PublishSourceTag creates the source tag Tag on Commit, and raises
	// Warning, when set, as the Warning Event SourceNotGoInstallable.
	PublishSourceTag PublishAction = "SourceTag"
	// PublishRetargetDraft points the draft Release at the commit Target.
	PublishRetargetDraft PublishAction = "RetargetDraft"
	// PublishCreateDraft creates the GitHub release Tag, named Name, as a draft
	// with Body, on the commit Target when it is set.
	PublishCreateDraft PublishAction = "CreateDraft"
	// PublishUploadAsset streams Gitea's Asset to the draft Release, deleting
	// the draft's Replace first when it is set.
	PublishUploadAsset PublishAction = "UploadAsset"
	// PublishDraft publishes the draft Release, as the repository's Latest
	// when Latest is set.
	PublishDraft PublishAction = "PublishDraft"
	// PublishDone records the release as published at URL.
	PublishDone PublishAction = "Done"
	// PublishFail reports why the release isn't published; the next poll tries
	// again.
	PublishFail PublishAction = "Fail"
)

// Reasons of the Published condition.
const (
	PublishPublished          = "Published"
	PublishWaiting            = "Waiting"
	PublishSecretMissing      = "SecretMissing"
	PublishSecretUnreadable   = "SecretUnreadable"
	PublishUnauthorized       = "Unauthorized"
	PublishRepositoryNotFound = "RepositoryNotFound"
	PublishEmptyRepository    = "EmptyRepository"
	PublishNoReleaseFiles     = "NoReleaseFiles"
	PublishTagTaken           = "TagTaken"
	PublishGitHubError        = "GitHubError"
	PublishSourceTagTaken     = "SourceTagTaken"
	PublishFolderOverlaps     = "FolderOverlaps"
	PublishSourceTooLarge     = "SourceTooLarge"
	PublishBranchProtected    = "BranchProtected"
	PublishRepositoryRenamed  = "RepositoryRenamed"
	PublishRepositoryReplaced = "RepositoryReplaced"
)

// PublishBranchMoved is the failure of a fast-forward GitHub refused because
// another commit landed on the default branch first: no reason of Published,
// since the next poll builds on the new head.
const PublishBranchMoved = "BranchMoved"

// PublishStep is the next step of publishing a release.
type PublishStep struct {
	Action PublishAction
	// Reason and Message are the condition's, for PublishWait, PublishFail and
	// PublishDone.
	Reason, Message string
	// Tag is the GitHub tag, and Body the notes for PublishWriteNotes or the
	// draft's body, marker included, for PublishCreateDraft.
	Tag, Body string
	// Name is the draft's name, for PublishCreateDraft.
	Name string
	// Release is the draft, for PublishUploadAsset and PublishDraft.
	Release int64
	// Asset is the Gitea asset to upload, and Replace the draft's asset of the
	// same name to delete first, or nil.
	Asset   ReleaseAsset
	Replace *ReleaseAsset
	// URL is the published release, for PublishDone.
	URL string
	// Latest says the release becomes the repository's Latest, for
	// PublishDraft.
	Latest bool
	// Target is the commit the draft's tag goes on, for PublishCreateDraft and
	// PublishRetargetDraft; "" leaves it to GitHub, the default branch.
	Target string
	// SourceCommit is the commit to build, for PublishSourceCommit.
	SourceCommit *SourceCommit
	// Commit is the commit the source tag goes on, and Warning why go install
	// can't fetch it, or "", for PublishSourceTag.
	Commit, Warning string
	// Source is the published source, for PublishDone when the Crew publishes
	// one and it is out.
	Source *PublishedSource
}

// GitHubTag is the tag a release goes out under on GitHub: the prefix, then
// the Crew's tag.
func GitHubTag(prefix, tag string) string {
	return prefix + tag
}

// ReleaseName is the name a published release goes by: the app, then the
// Crew's tag, so a repository several apps publish to says whose each is.
func ReleaseName(app, tag string) string {
	if app == "" {
		return tag
	}
	return app + " " + tag
}

// PublishMarker is the hidden line that ends the body of a release the
// operator publishes, so a retry finds its draft and knows the release is its own.
func PublishMarker(namespace, container, tag string) string {
	return ShipMarker(namespace+"/"+container, tag)
}

// ShipMarker is PublishMarker of app, namespace/name, at tag.
func ShipMarker(app, tag string) string {
	return fmt.Sprintf("<!-- c4factory: %s %s -->", app, tag)
}

// ShipMarkerPrefix is the start of every ShipMarker of app, whatever its tag.
func ShipMarkerPrefix(app string) string {
	return fmt.Sprintf("<!-- c4factory: %s ", app)
}

// ReleaseNotes are the notes the operator writes for a release nobody wrote
// notes for: the merged issues' titles, with no links into the Git host.
func ReleaseNotes(issues []ReleaseIssue) string {
	var b strings.Builder
	for _, i := range issues {
		fmt.Fprintf(&b, "- %s (#%d)\n", i.Title, i.Number)
	}
	return b.String()
}

// MergedReleaseIssues returns the release's closed issues whose work merged,
// meaning a Merged Task did them (one a Standard change did included), in the
// order given. An issue closed without a merge, by prompt, as not doable or as
// a duplicate, isn't in the release, so its notes leave it out.
func MergedReleaseIssues(closed []ReleaseIssue, merged []int) []ReleaseIssue {
	out := []ReleaseIssue{}
	for _, i := range closed {
		if slices.Contains(merged, i.Number) {
			out = append(out, i)
		}
	}
	return out
}

// NextPublishStep decides the next step of publishing the Crew's last release.
// The operator takes it, adds what it read to the facts, and asks again until a
// step that writes or reports ends the pass: one write per poll. A failed call
// goes into Failure, and the next poll starts over, carrying on where the last
// one stopped.
func NextPublishStep(f PublishFacts) PublishStep {
	tag := GitHubTag(f.TagPrefix, f.Tag)
	switch {
	case !f.Configured:
		return PublishStep{Action: PublishNone}
	case f.Published:
		return PublishStep{Action: PublishNone}
	case f.Runs != RunSucceeded:
		return PublishStep{Action: PublishWait, Reason: PublishWaiting, Message: fmt.Sprintf(
			"%s is published once its release runs succeed; they are %s", f.Tag, runState(f.Runs))}
	case f.SecretError != "":
		return fail(PublishSecretUnreadable, "Secret %s can't be read: %s; %s is published once it can",
			f.Secret, f.SecretError, f.Tag)
	case !f.SecretFound:
		return fail(PublishSecretMissing, "Secret %s with the key token (or a GitHub App's %s) is missing; %s is "+
			"published once it is there", f.Secret, strings.Join(GitHubAppKeys, ", "), f.Tag)
	case f.Failure != nil:
		return failed(f, tag)
	case f.Source != nil && len(f.Overlaps) > 0:
		return fail(PublishFolderOverlaps, "source folder %s of %s overlaps that of Crew %s; nothing is published until one moves",
			f.Source.Path, f.Repository, strings.Join(f.Overlaps, ", "))
	case f.Gitea == nil:
		return PublishStep{Action: PublishReadGitea}
	case f.Gitea.Missing:
		return fail(PublishNoReleaseFiles, "Gitea has no release %s to publish", f.Tag)
	case len(f.Gitea.Assets) == 0:
		return fail(PublishNoReleaseFiles, "Gitea release %s has no files to publish", f.Tag)
	}
	if step, ok := repositoryStep(f); !ok {
		return step
	}
	if f.GitHub == nil {
		return PublishStep{Action: PublishReadGitHub}
	}

	marker := PublishMarker(f.Namespace, f.Container, tag)
	ours := findMarked(f.GitHub.Releases, marker)
	// The source tag may be the release's own: then the release takes the tag
	// the source made.
	sharedTag := f.Source != nil && SourceTag(*f.Source, f.Tag) == tag
	switch {
	case ours != nil && !ours.Draft:
		return doneStep(f, tag, ours.URL)
	case ours == nil && (f.GitHub.TagExists && !sharedTag || hasTag(f.GitHub.Releases, tag)):
		return fail(PublishTagTaken, "%s already has a release or tag %s the factory didn't make; it leaves it alone",
			f.Repository, tag)
	}

	// The source goes out first, so the release's tag goes on its commit.
	target := ""
	if f.Source != nil {
		step, src := sourceStep(f, tag)
		if src == nil {
			return step
		}
		target = src.Commit
	}
	if ours != nil {
		return NextReleaseStep(ReleaseShip{Commit: target, Files: f.Gitea.Assets, Latest: f.Latest}, ours)
	}

	notes := f.Gitea.Body
	if strings.TrimSpace(notes) == "" {
		if f.Issues == nil {
			return PublishStep{Action: PublishReadIssues}
		}
		// With no issue to name there are no notes to write, and a body set
		// to "" is ignored, so the draft carries the marker alone.
		if len(f.Issues) > 0 {
			return PublishStep{Action: PublishWriteNotes, Tag: f.Tag, Body: ReleaseNotes(f.Issues)}
		}
	}
	return NextReleaseStep(ReleaseShip{Tag: tag, Name: ReleaseName(f.App, f.Tag), Body: ReleaseBody(notes, marker),
		Commit: target}, nil)
}

// ReleaseShip is a release shipped as a GitHub release: its tag, name and
// body, marker included, the commit its tag goes on ("" for the default
// branch's head), its files and whether it becomes the repository's Latest.
type ReleaseShip struct {
	Tag, Name, Body, Commit string
	Files                   []ReleaseAsset
	Latest                  bool
}

// NextReleaseStep is the next write of the draft, upload and publish sequence
// that ships r, given ours, the release whose body holds r's marker, nil when
// there is none: create the draft, retarget it to r's commit, upload the first
// file it lacks or has at another size, or publish it once it has them all.
// A published release is PublishDone at its URL.
func NextReleaseStep(r ReleaseShip, ours *TargetRelease) PublishStep {
	switch {
	case ours == nil:
		return PublishStep{Action: PublishCreateDraft, Tag: r.Tag, Name: r.Name, Body: r.Body, Target: r.Commit}
	case !ours.Draft:
		return PublishStep{Action: PublishDone, URL: ours.URL}
	case r.Commit != "" && ours.Target != r.Commit:
		return PublishStep{Action: PublishRetargetDraft, Release: ours.ID, Target: r.Commit}
	}
	return draftStep(r.Files, ours, r.Latest)
}

// ReleaseBody is the body of a release the operator ships: its notes, then
// marker on a line of its own.
func ReleaseBody(notes, marker string) string {
	body := marker + "\n"
	if strings.TrimSpace(notes) != "" {
		body = strings.TrimRight(notes, "\n") + "\n\n" + body
	}
	return body
}

// repositoryStep reads the repository GitHub answers for the target, and
// fails a target renamed or replaced since the Crew published to it; ok when
// it is the same.
func repositoryStep(f PublishFacts) (PublishStep, bool) {
	switch {
	case f.Remote == nil:
		return PublishStep{Action: PublishReadRepository}, false
	case !strings.EqualFold(f.Remote.Repository, f.Repository):
		// GitHub followed a rename: the releases would go on under the old name.
		return fail(PublishRepositoryRenamed, "%s was renamed to %s; point %s at %s, or at another repository; "+
			"nothing is published until then", f.Repository, f.Remote.Repository, setBy(f), f.Remote.Repository), false
	case replaced(f):
		return fail(PublishRepositoryReplaced, "%s is not the repository this Crew published to before "+
			"(GitHub id %d, now %d): it was deleted or renamed and a new one took its name. Nothing is published "+
			"to it until you point %s at the repository wanted, or accept this one with the annotation "+
			"c4.factory.io/accept-repository: \"%d\" on the Crew",
			f.Repository, f.Recorded.ID, f.Remote.ID, setBy(f), f.Remote.ID), false
	}
	return PublishStep{}, true
}

// replaced says the repository GitHub answers for Repository is another than
// the one the Crew published to under that name, and no person accepted it.
func replaced(f PublishFacts) bool {
	return f.Recorded != nil && strings.EqualFold(f.Recorded.Repository, f.Repository) &&
		f.Recorded.ID != f.Remote.ID && f.Accept != strconv.FormatInt(f.Remote.ID, 10)
}

// NextPublishTarget is the repository the Crew records as published to
// (status.publishTarget) after a pass that read f and ended in step, and
// whether the pass took Accept, so the annotation can go. A target changed to
// another repository drops the record; the first publish that succeeds sets
// it, and an accepted id replaces it.
func NextPublishTarget(f PublishFacts, step PublishStep) (*PublishTarget, bool) {
	rec := f.Recorded
	if rec != nil && !strings.EqualFold(rec.Repository, f.Repository) {
		rec = nil
	}
	if f.Remote == nil || !strings.EqualFold(f.Remote.Repository, f.Repository) {
		return rec, false
	}
	accepted := f.Accept != "" && f.Accept == strconv.FormatInt(f.Remote.ID, 10)
	if accepted || rec == nil && step.Action == PublishDone {
		rec = &PublishTarget{Repository: f.Repository, ID: f.Remote.ID}
	}
	return rec, accepted
}

// setBy names where the target repository is set, for a message.
func setBy(f PublishFacts) string {
	if f.Brief != "" {
		return fmt.Sprintf("Brief %s's spec.releases", f.Brief)
	}
	return "the Crew's spec.publishReleases"
}

// doneStep records the release as published at url, with its source when the
// Crew publishes one and its tag is out. A release published before the Crew
// set source has none: source applies from the next release.
func doneStep(f PublishFacts, tag, url string) PublishStep {
	step := PublishStep{Action: PublishDone, Reason: PublishPublished, URL: url,
		Message: fmt.Sprintf("%s is published at %s", tag, url)}
	if f.Source == nil {
		return step
	}
	switch {
	case f.SourceFiles == nil:
		return PublishStep{Action: PublishReadSource}
	case f.GitHubSource == nil:
		return PublishStep{Action: PublishReadGitHubSource}
	}
	// A tag on another folder tree isn't this release's source.
	if c := f.GitHubSource.TagCommit; c != "" && f.SourceFiles != nil && f.GitHubSource.TagTree == f.SourceFiles.Tree {
		sourceTag := SourceTag(*f.Source, f.Tag)
		step.Source = &PublishedSource{Commit: c, Tag: sourceTag, URL: SourceURL("", f.Repository, f.Source.Path, sourceTag)}
		step.Message += fmt.Sprintf(", its source at %s", step.Source.URL)
	}
	return step
}

// draftStep uploads the first of Gitea's assets the draft lacks, or has at a
// different size, and publishes the draft once it has them all, as Latest when
// latest is set.
func draftStep(assets []ReleaseAsset, draft *TargetRelease, latest bool) PublishStep {
	for _, a := range assets {
		var have *ReleaseAsset
		for i, d := range draft.Assets {
			if d.Name == a.Name {
				have = &draft.Assets[i]
				break
			}
		}
		switch {
		case have == nil:
			return PublishStep{Action: PublishUploadAsset, Release: draft.ID, Asset: a}
		case have.Size != a.Size:
			return PublishStep{Action: PublishUploadAsset, Release: draft.ID, Asset: a, Replace: have}
		}
	}
	return PublishStep{Action: PublishDraft, Release: draft.ID, Latest: latest}
}

// failed reports the GitHub call that failed in this pass.
func failed(f PublishFacts, tag string) PublishStep {
	detail := ""
	if f.Failure.Detail != "" {
		detail = ": " + f.Failure.Detail
	}
	switch f.Failure.Reason {
	case PublishUnauthorized:
		return fail(PublishUnauthorized, "GitHub refused the token in Secret %s for %s: the token can't write "+
			"releases to %s. Check that %s is where releases should go (set by %s) and that the token is for %s "+
			"with Contents read and write%s", f.Secret, f.Repository, f.Repository, f.Repository, setBy(f),
			f.Repository, detail)
	case PublishRepositoryNotFound:
		return fail(PublishRepositoryNotFound, "GitHub has no repository %s the token in Secret %s can see%s",
			f.Repository, f.Secret, detail)
	case PublishEmptyRepository:
		return fail(PublishEmptyRepository, "%s has no default branch to tag %s at; push a first commit%s",
			f.Repository, tag, detail)
	case PublishTagTaken:
		return fail(PublishTagTaken, "%s already has a release or tag %s the factory didn't make; it leaves it alone%s",
			f.Repository, tag, detail)
	case PublishSourceTooLarge:
		return fail(PublishSourceTooLarge, "GitHub refused a file of the source of %s as too large%s", f.Tag, detail)
	case PublishBranchProtected:
		return fail(PublishBranchProtected, "the default branch of %s is protected, so the source of %s can't go on it; "+
			"let the token's user push to it, or drop the protection%s", f.Repository, f.Tag, detail)
	case PublishBranchMoved:
		return PublishStep{Action: PublishWait, Reason: PublishWaiting, Message: fmt.Sprintf(
			"another commit landed on the default branch of %s first; the next poll puts the source of %s on it",
			f.Repository, f.Tag)}
	}
	return fail(PublishGitHubError, "GitHub failed publishing %s to %s%s", tag, f.Repository, detail)
}

func fail(reason, format string, args ...any) PublishStep {
	return PublishStep{Action: PublishFail, Reason: reason, Message: fmt.Sprintf(format, args...)}
}

// findMarked returns the release whose body holds marker, a published one
// before a draft, or nil.
func findMarked(releases []TargetRelease, marker string) *TargetRelease {
	var draft *TargetRelease
	for i, r := range releases {
		if !strings.Contains(r.Body, marker) {
			continue
		}
		if !r.Draft {
			return &releases[i]
		}
		if draft == nil {
			draft = &releases[i]
		}
	}
	return draft
}

func hasTag(releases []TargetRelease, tag string) bool {
	for _, r := range releases {
		if r.TagName == tag {
			return true
		}
	}
	return false
}

// runState says how a release's runs are, for a message.
func runState(result string) string {
	switch result {
	case RunWaiting, "":
		return "not started"
	case RunFailed:
		return "failed, and it waits for a re-run to succeed"
	}
	return strings.ToLower(result)
}

// ShipCommit is the commit the release's tag goes on: its source's, once the
// Crew put the source out, else "" for the default branch's head.
func ShipCommit(f PublishFacts) string {
	if f.Source == nil {
		return ""
	}
	if _, src := sourceStep(f, GitHubTag(f.TagPrefix, f.Tag)); src != nil {
		return src.Commit
	}
	return ""
}

// ShippedFileName is the name, among files, of the file GitHub stored as name:
// GitHub turns a space, and other characters it doesn't keep in a file name,
// into a dot. A name no file maps to is itself.
func ShippedFileName(files []ReleaseAsset, name string) string {
	for _, a := range files {
		if GitHubAssetName(a.Name) == name {
			return a.Name
		}
	}
	return name
}

// ImageArchiveSuffix ends the name of the release file that holds the
// release's image, an OCI image layout tar (docs/plans/images.md).
const ImageArchiveSuffix = ".oci.tar"

// IsImageArchive reports whether a release file is the release's image
// archive. The operator pushes it to the Git host's registry, and the
// release copy to GitHub leaves it out: an image belongs in a registry,
// and a deploy copies it to GHCR (#1070).
func IsImageArchive(name string) bool { return strings.HasSuffix(name, ImageArchiveSuffix) }

// GitHubAssetName is the name GitHub gives a file uploaded as name.
func GitHubAssetName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', strings.ContainsRune("._+-", r):
			return r
		}
		return '.'
	}, name)
}
