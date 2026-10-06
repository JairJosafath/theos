package model

import (
	"fmt"
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
	// tag, and TagPrefix what goes before it on GitHub.
	Namespace, Container, Tag, TagPrefix string
	// Repository is the GitHub repository, owner/name.
	Repository string
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
}

// PublishFailure is a GitHub call that failed, with its reason (one of
// PublishUnauthorized, PublishRepositoryNotFound, PublishEmptyRepository,
// PublishTagTaken, PublishGitHubError) and what GitHub said.
type PublishFailure struct {
	Reason, Detail string
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
	Draft   bool
	Body    string
	URL     string
	Assets  []ReleaseAsset
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
	// PublishReadGitHub reads the GitHub repository's releases into GitHub.
	PublishReadGitHub PublishAction = "ReadGitHub"
	// PublishWait waits for the release's runs to succeed.
	PublishWait PublishAction = "Wait"
	// PublishWriteNotes sets Body on the Gitea release, so both releases read
	// the same.
	PublishWriteNotes PublishAction = "WriteNotes"
	// PublishCreateDraft creates the GitHub release Tag, named Tag, as a draft
	// with Body.
	PublishCreateDraft PublishAction = "CreateDraft"
	// PublishUploadAsset streams Gitea's Asset to the draft Release, deleting
	// the draft's Replace first when it is set.
	PublishUploadAsset PublishAction = "UploadAsset"
	// PublishDraft publishes the draft Release.
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
)

// PublishStep is the next step of publishing a release.
type PublishStep struct {
	Action PublishAction
	// Reason and Message are the condition's, for PublishWait, PublishFail and
	// PublishDone.
	Reason, Message string
	// Tag is the GitHub tag, and Body the notes for PublishWriteNotes or the
	// draft's body, marker included, for PublishCreateDraft.
	Tag, Body string
	// Release is the draft, for PublishUploadAsset and PublishDraft.
	Release int64
	// Asset is the Gitea asset to upload, and Replace the draft's asset of the
	// same name to delete first, or nil.
	Asset   ReleaseAsset
	Replace *ReleaseAsset
	// URL is the published release, for PublishDone.
	URL string
}

// GitHubTag is the tag a release goes out under on GitHub: the prefix, then
// the Crew's tag.
func GitHubTag(prefix, tag string) string {
	return prefix + tag
}

// PublishMarker is the hidden line that ends the body of a release the
// operator publishes, so a retry finds its draft and knows the release is its own.
func PublishMarker(namespace, container, tag string) string {
	return fmt.Sprintf("<!-- c4factory: %s/%s %s -->", namespace, container, tag)
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
		return fail(PublishSecretMissing, "Secret %s with the key token is missing; %s is published once it is there",
			f.Secret, f.Tag)
	case f.Failure != nil:
		return failed(f, tag)
	case f.Gitea == nil:
		return PublishStep{Action: PublishReadGitea}
	case f.Gitea.Missing:
		return fail(PublishNoReleaseFiles, "Gitea has no release %s to publish", f.Tag)
	case len(f.Gitea.Assets) == 0:
		return fail(PublishNoReleaseFiles, "Gitea release %s has no files to publish", f.Tag)
	case f.GitHub == nil:
		return PublishStep{Action: PublishReadGitHub}
	}

	marker := PublishMarker(f.Namespace, f.Container, tag)
	ours := findMarked(f.GitHub.Releases, marker)
	switch {
	case ours != nil && !ours.Draft:
		return PublishStep{Action: PublishDone, Reason: PublishPublished, URL: ours.URL,
			Message: fmt.Sprintf("%s is published at %s", tag, ours.URL)}
	case ours != nil:
		return draftStep(f.Gitea.Assets, ours)
	case f.GitHub.TagExists || hasTag(f.GitHub.Releases, tag):
		return fail(PublishTagTaken, "%s already has a release or tag %s the factory didn't make; it leaves it alone",
			f.Repository, tag)
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
	body := marker + "\n"
	if strings.TrimSpace(notes) != "" {
		body = strings.TrimRight(notes, "\n") + "\n\n" + body
	}
	return PublishStep{Action: PublishCreateDraft, Tag: tag, Body: body}
}

// draftStep uploads the first of Gitea's assets the draft lacks, or has at a
// different size, and publishes the draft once it has them all.
func draftStep(assets []ReleaseAsset, draft *TargetRelease) PublishStep {
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
	return PublishStep{Action: PublishDraft, Release: draft.ID}
}

// failed reports the GitHub call that failed in this pass.
func failed(f PublishFacts, tag string) PublishStep {
	detail := ""
	if f.Failure.Detail != "" {
		detail = ": " + f.Failure.Detail
	}
	switch f.Failure.Reason {
	case PublishUnauthorized:
		return fail(PublishUnauthorized, "GitHub refused the token in Secret %s for %s%s",
			f.Secret, f.Repository, detail)
	case PublishRepositoryNotFound:
		return fail(PublishRepositoryNotFound, "GitHub has no repository %s the token in Secret %s can see%s",
			f.Repository, f.Secret, detail)
	case PublishEmptyRepository:
		return fail(PublishEmptyRepository, "%s has no default branch to tag %s at; push a first commit%s",
			f.Repository, tag, detail)
	case PublishTagTaken:
		return fail(PublishTagTaken, "%s already has a release or tag %s the factory didn't make; it leaves it alone%s",
			f.Repository, tag, detail)
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
