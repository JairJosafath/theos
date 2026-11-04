# A Brief's release target: said where it's set, never silently undone, checked before publishing

Issue [#989](https://github.com/JairJosafath/c4factory/issues/989). Agent: otter-989.

## Goal

gray-lenovo renamed their private repository `JairJosafath/cel-lens` to
`JairJosafath/private-cel-lens`, made a new public repository under the old
name, and changed both app Crews' `spec.publishReleases.github.repository` on
the Crew page. Within minutes the Brief's `spec.releases`, which still named
the old repository, had put it back (`syncReleases` in
`internal/controller/brief_release.go`), with no Event, no condition and
nothing on the Crew page. The next release failed with `GitHub refused the
token in Secret cel-lens-github for JairJosafath/cel-lens ... (HTTP 403)`. A
broader token would have published raw releases into the new, hand-curated
public repository.

Done when:
- a Brief-made Crew whose target comes from the Brief says so, on the Crew
  (a condition, so `kubectl describe crew` shows it) and on its page, and the
  page's release form changes the Brief's `spec.releases` entry, not the Crew;
- a change made on the Crew itself is neither undone nor used: the Brief
  leaves it, the Crew publishes nothing while the two disagree, and the Crew,
  the Brief's *Needs you* and an Event say so and how to settle it;
- a publish refused by GitHub names the target repository as a cause, not only
  the token, and a target repository renamed or replaced since the Crew last
  published there stops publishing before anything is written to it.

## Decisions

Taken by otter-989 in a headless session; the user can revisit them on the
pull request.

- **The Brief owns the target it sets.** A `spec.releases` entry is the
  person's statement of where an app's releases go, made on the Brief; the
  Brief keeps pushing it to the Crew, so a later change of the entry reaches
  the Crew without a round (as today). The Crew page and `kubectl` must say
  that, and the page edits the entry where it lives. The Brief's card and
  `status.apps[].publishTo` already show the target; the Crew is where people
  look and where gray-lenovo changed it.
- **Held and flagged, not refused at admission.** Refusing a person's change
  to a Brief-made Crew's `spec.publishReleases` would need a
  ValidatingAdmissionPolicy that tells the operator's write from a person's.
  It can only do that by the operator's ServiceAccount name, which differs
  between the kustomize install (`theos-controller-manager` in
  `theos-system`), the chart (`resourceName`, or a `serviceAccount.name`
  the user picks) and envtest; an `authorizer` check would let any
  cluster-admin, the usual `kubectl` user on a Kind cluster, through anyway.
  A rule that breaks with an install option is worse than none. Instead the
  Brief remembers the target it last set and treats any other value on the
  Crew as a person's change: it leaves the Crew alone and flags it, and the
  Crew publishes nothing until the two agree. Nothing is silently undone, and
  no release goes to a target only one side names. The UI, where most such
  changes are made, edits the Brief, so it never makes the disagreement.
- **The last target the Brief set is an annotation on the Crew.**
  `theos.factory.io/brief-publish-releases` holds the JSON of the
  `CrewPublishReleases` the Brief last wrote (`{}` when its entry names none),
  written in the same patch as the spec. Comparing three values settles who
  changed what:
  - entry ≠ annotation: the Brief's entry changed (or is new): the Brief
    writes the entry and the annotation, as today. The newest act on the
    owner wins, which is also how a person settles a disagreement by editing
    the Brief.
  - entry = annotation ≠ Crew: the Crew was changed by someone else: the Brief
    leaves it, and the Crew is held.
  - all three equal: in sync.
  - no annotation (Crews made before this change): a Crew with no
    `publishReleases`, or one equal to the entry, gets the entry and the
    annotation; one with another target is held, since the factory can't know
    who set it.
  The Crew carries the fact, so the Crew controller holds publishing without
  reading the Brief. Settling: put the Crew back (`kubectl edit`, or remove
  the change), or change the Brief's entry to the target wanted; either makes
  the three agree.
- **One condition on the Crew says who sets the target.** `ReleaseTarget`,
  set only on a Crew with the annotation: True, reason `SetByBrief`, message
  "Brief cel-lens sets this in spec.releases; change it there"; False, reason
  `DiffersFromBrief`, message naming both repositories and the two ways to
  settle it. While False, `Published` is False with reason
  `TargetDiffersFromBrief` and nothing is published (the release waits, as a
  failed publish does, and goes out at the next poll after it is settled); a
  Warning Event `ReleaseTargetDiffers` goes out once per Crew target. The
  Brief lists the Crew in `status.needsYou`, kind `Crew`, recommending to
  change `spec.releases` to the Crew's target (the person's latest act), why
  "the Crew was changed to <B> but the Brief still says <A>; nothing is
  published until they agree". No autopilot answers it: only a person knows
  which repository is right.
- **The Crew page edits the Brief's entry.** For a Crew whose
  `ReleaseTarget` condition is set, the page's publish line says "set by
  Brief <name>" with a link, and the release form (`ReleaseForm`) writes the
  Brief's `spec.releases[i]` through the Brief JSON patch the UI server
  already allows (a `test` of the Brief's `resourceVersion` first). A held
  Crew shows both targets and the two answers as buttons: use the Crew's
  target (writes the Brief's entry) or put the Brief's back (writes the
  Crew). `tagReleases`/`prepareReleases` stay on the Crew, which the Brief
  only ever turns on.
- **A refused publish names the target.** GitHub answers 403 "Resource not
  accessible by personal access token" both for a token too narrow and for a
  token made for another repository, which is what a repository replaced
  under the same name looks like. The `Unauthorized` message becomes "GitHub
  refused the token in Secret S for R: the token can't write releases to R.
  Check that R is where releases should go (set by Brief B's spec.releases /
  the Crew's spec.publishReleases) and that the token is for R with Contents
  read and write". `model.failed` gets who sets the target in its facts.
- **The Crew remembers the repository it published to, by GitHub's id.**
  `status.publishTarget` (`repository`, `id`) is set from the repository
  GitHub returns at the first publish that succeeds. Each publish already
  reads the repository (`DefaultBranch`, `GET /repos/{owner}/{name}`); it now
  also reads `id` and `full_name`. Before anything is written:
  - `full_name` differs from the target (GitHub followed a rename): fail
    `RepositoryRenamed`, "R was renamed to R2; point the target at R2, or at
    another repository". GitHub redirects a renamed repository's API calls,
    so without this the Crew would keep publishing under the old name.
  - same name, other `id` than recorded (the repository was replaced, as
    gray-lenovo's was): fail `RepositoryReplaced`, "R is not the repository
    this Crew published to before (GitHub id N, now M); it was deleted or
    renamed and a new one took its name". A person who means the new one says
    so with the annotation `theos.factory.io/accept-repository: <M>` on the
    Crew; the operator records M and removes the annotation.
  - a target changed on purpose (another `repository`) resets the record.
  Both are Warning `ReleasePublishFailed` Events, once per reason, like the
  other failures, and never write to the repository.

## Steps

Each is its own issue and pull request, and leaves `main` working.

1. **A publish names its target and checks the repository is the same one (#990).**
   `internal/model/publish.go` (the `Unauthorized` message naming the target
   and who sets it; `RepositoryRenamed` and `RepositoryReplaced` from the
   facts), `internal/gitprovider/github_source.go` (the repository read
   returns `id` and `full_name`), `status.publishTarget` on the Crew, the
   accept annotation, the Crew page's failure text. Table tests for each
   reason; gitprovider tests with a faked GitHub (renamed: a redirect;
   replaced: another id); envtest: a replaced repository is not written to,
   the annotation accepts it.
2. **The Brief remembers the target it set and holds a Crew changed under
   it (#991).** The annotation, the three-way rule in `releaseCrew` and at creation
   (`brief_apply.go`), the `ReleaseTarget` condition, `Published`
   `TargetDiffersFromBrief`, the Event, the Brief's `needsYou` Crew item.
   Envtest: an entry change reaches the Crew; a Crew change is left, held and
   flagged; settling either way clears it; a Crew made before the annotation
   is adopted or held as above.
3. **The Crew page says the Brief sets the target and edits it there (#992).**
   `CrewPage`, `ReleaseForm`, the lifecycle text, the two answers for a held
   Crew, the demo fixture. Vitest for each state. Depends on step 2.

`docs/design.md` says all of this as of this plan; a step that changes a
decision changes the design with it.
