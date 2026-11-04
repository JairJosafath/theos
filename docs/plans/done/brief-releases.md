# Plan: a Brief's apps get releases, and a Brief says where they go

## Goal

An app made from a Brief is released without operating the factory. When the
planner says it ships a Container as a release, the Crew the Brief made tags
and prepares releases, and the round's issues for that Container go into a
release milestone, so a release follows once they are done. When the plan
doesn't release a Container, the Brief says so in plain words and how to turn
releases on. A person names on the Brief the GitHub repository a Container's
releases are published to, with a Secret holding a token for that repository
only (theirs to make, like the Claude token). The Brief's status, and its card
in the UI, show each app's latest release and where it went.

Reported as #374 by `gray-lenovo`: their warm-up Brief made Sprout, a Go CLI,
whose plan said "I ship it as a Gitea release", and no release was ever made:
the Crew the Brief made had releases off (`docs/plans/done/brief.md`, B9) and
its issues were filed in no milestone (B11). Their real app, cel-lens, must
publish its releases to `github.com/JairJosafath/cel-lens` (#249's target), and
a Brief had no place to say so.

## Decisions

| # | Topic | Decision | Why |
| --- | --- | --- | --- |
| R1 | The planner says it | An optional plan file `plan/releases.yaml`, `containers: [sprout]`, names the Containers that ship releases. Each must be a Container of the plan's model, else the plan is refused with the reason. A file that is absent, or lists nothing, changes nothing. | The story model stays plain manifests (a Container has no release field, and the Crew is not the planner's to write, B6); `choices.md` is prose for people, not something to parse. A file of its own is checked like the rest of the plan. |
| R2 | Releases only turn on | A Container released once stays released: a later plan that leaves it out changes nothing, and the Brief never turns a Crew's releases off. A person who wants them off edits the Crew. | Tags are never moved or deleted; turning releases off is rare and is the person's, on the Crew. |
| R3 | Who turns them on | The plan (R1), or the person: a follow-up prompt ("release it") the planner answers with R1, or an entry in `spec.releases` (R5) naming the Container. | A prompt is how a person steers a Brief; `spec.releases` is the one place a credential is named. |
| R4 | The Crew | For a released Container whose Crew the Brief made (label `theos.factory.io/brief` of this Brief), the Brief sets `tagReleases: true` and `prepareReleases: true` on it, at creation or later. A Crew it didn't make is left alone, and the Brief's status says releases are that Crew's owner's. | Tagging and preparing cost no money and touch no access, so B9's reason doesn't hold for them; the Crew still keeps its Agents and size, which B9 keeps from the planner. Preparing writes the version files and a changelog; its acceptance criteria already allow a template without `make release-version`. |
| R5 | Where releases go | `spec.releases`, a list of at most 10, keyed by `container`: `{container, github: {repository: owner/name, tokenSecretRef: {name}}, tagPrefix}`, the same fields as the Crew's `spec.publishReleases` (#249), the Secret in the Brief's namespace with key `token`. Mutable. An entry turns releases on for its Container (publishing needs tagging) and sets the Brief-made Crew's `publishReleases` to match; an entry removed later leaves the Crew as it is, and what is on GitHub stays. The operator writes the entries into the workspace's `brief.md`, so the planner names that Container and releases it. | The person hands over access, as with the Claude token; the Brief only points the Crew at it, and the operator, not CI, still publishes (#249), so the token never reaches the agents' code. Clearing a Crew's target on removal could undo a person's own edit of the Crew. |
| R6 | The release milestone | A round files a released Container's issues in a milestone of its own, titled the next minor version after the newest `vMAJOR.MINOR.PATCH` milestone of the repository, open or closed (`v0.1.0` when there is none), due seven days after it is made, its description ending `<!-- c4-brief: <brief>/<round> -->`. A restart finds it again by that marker, so it never makes a second one. A Container not released keeps B11: no milestone. Issues filed before releases were turned on stay where they are. | A release is a dated `vX.Y.Z` milestone (design, *Releases go one at a time*); the due date only orders releases. One release per round keeps each round's work in its own tag. |
| R7 | A release needs work | The planner's guide says a round that turns releases on for a Container files at least one issue for it, since a milestone with no merged issue is never released. A round that releases a Container but files nothing for it makes no milestone, and the app's line in the status says the next release waits for work. | The Crew prepares and tags only a release with merged work (`prepare-release.md`). |
| R8 | Status | `status.apps`, one per Container the Brief created: `container`, `crew`, `releases` (`On` or `Off`), `publishTo` (`owner/name` or empty), `lastRelease` (`tag`, `url` of the Gitea release page, `time`, `run`, `publishState`, `publishedURL`) copied from the Crew's `status.lastRelease`, and `message`, one plain sentence. With releases off it reads *Releases are off: ask for them in a prompt, or name this app in spec.releases.* A `spec.releases` entry naming no Container the Brief made shows as an app line with no Crew, saying the plan hasn't made it. The Brief watches the Crews it made, so a new release shows within a poll. | The issue asks for the card to show the latest release and where it went; the Crew already holds every fact, so the Brief copies, it doesn't decide. |
| R9 | The card | The UI's Brief card lists `status.apps`: each app's releases on or off, its latest tag linking to the Gitea release, and where it was published, linking to the GitHub release. The UI writes nothing new: `spec.releases` is set with kubectl or GitOps, like the Secret it names. | The UI server writes only a Brief and its prompts (design, *Briefs*). |
| R10 | The stand-in planner | Releases nothing; `make e2e` is unchanged. | The release loop is already covered by the Crew's tests; the Brief's part is covered by envtest with the fake provider. |

## Steps

Each step is its own pull request and leaves main working. Nothing reads
`plan/releases.yaml` or `spec.releases` until step 4.

1. **The design.** (#374) This plan and `docs/design.md` (*Briefs*: releases).
   *Done when:* merged.
2. **The model.** (#387) `internal/model`: `Plan.Releases` parsed from
   `plan/releases.yaml` and checked (R1: a name not a Container of the plan
   is refused, an unknown key is refused); `ReleaseMilestoneTitle`, the next
   minor title from a repository's milestone titles (R6), and the milestone
   marker; the workspace's `brief.md` lists `spec.releases` entries and
   `model/containers.yaml` says whether each Container is released (taken as
   inputs, so the controller fills them in step 4); `planner.md` says when
   and how to write `releases.yaml` (R1, R7).
   *Done when:* `make check` passes with table tests for each refusal, the
   title rule (none, `v0.1.0` open, `v0.3.2` closed with `v0.2.0` open, titles
   that aren't versions ignored) and the workspace files.
3. **Gitea's side.** (#388) `internal/gitprovider`: list a repository's milestones
   open and closed with their descriptions, and create one with a due date
   and a description (R6); local-Gitea tests. Gitea clears the due date of a
   milestone an edit gives none, so closing one sends its due date back, which
   Gitea keeps to the end of its day.
   *Done when:* `make test-gitea` creates a dated milestone and finds it again
   by its description, and `make check` passes.
4. **The Brief releases.** (#390) API: `spec.releases` with its CEL rules (unique
   `container`, DNS label, `owner/name`), `make manifests generate`; the
   controller: a released Container's Brief-made Crew gets `tagReleases`,
   `prepareReleases` and `publishReleases` (R4, R5), its round's issues go
   into the release milestone (R6), and so do the reviewers' suggestions the
   round takes for it (#375 takes them into no milestone, `answerSuggestion`
   in `brief_suggestions.go`), the workspace gets the inputs of step 2;
   envtest with the fake provider: a plan releasing its Container makes a
   releasing Crew and files the issues in `v0.1.0`; a second round files in
   `v0.2.0`; a restart after making the milestone makes no second one; a
   `spec.releases` entry sets the Crew's `publishReleases`; a Crew the Brief
   didn't make is untouched; the CEL rules' failing cases in
   `make validate-yaml` (`test/invalid`). `docs/getting-started.md`: *Release
   a Brief's app* (the prompt, the entry, the Secret).
   *Done when:* those envtest specs and `make check` pass.
5. **The Brief's status.** (#391) `status.apps` (R8), the Brief watching the Crews
   it made, `kubectl get brief` printing the apps' last tags; envtest: a Crew
   whose `status.lastRelease` changes shows on the Brief, releases off says
   how to turn them on, an entry for an app not made says so.
   *Done when:* those specs and `make check` pass.
6. **The card.** (#392) `ui/`: the Brief card shows `status.apps` (R9), with the demo
   fixture holding one released and published app and one with releases off;
   UI tests.
   *Done when:* the UI tests pass and a screenshot of the demo card is in
   `docs/screenshots/`.

What only a real cluster shows (a real planner choosing to release, a Go CLI's
release run attaching binaries, a publish to the reporter's
`JairJosafath/cel-lens` with their token) is the reporter's run on their
device, once step 5 ships in a release.

## Done when

The six steps are merged, and #374's reporter can follow *Release a Brief's
app* in `docs/getting-started.md` to have Sprout released in Gitea and
cel-lens's releases published to their GitHub repository, with nothing else
to know.
