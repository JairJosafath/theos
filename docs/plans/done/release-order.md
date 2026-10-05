# Crews work releases one at a time

**Goal:** A person can ready several releases at once and the Crew still finishes
them in order, so each release tag holds only its own issues.

**Why:** The Crew picks ready issues lowest number first, whatever their milestone.
On 3 October the server's v0.1.1 (#58 #59 #60) and v0.2.0 (#44 #45 #49 #50) were
both ready; `cel-language-server-44` started first, so v0.1.1's tag would carry a
v0.2.0 change. The workaround, keeping later releases `status/proposed` until the
earlier one closes, needs a person at every release boundary.

## The rule

- A milestone with a due date is a release. Releases go in due-date order (title
  breaks a tie); issues in an undated milestone, or in none, come after every
  release.
- The current release is the first one that still has a ready issue (startable or
  waiting on dependencies) or an issue a Task is working on.
- Only the current release's ready issues start, lowest number first after a
  Standard change with its allowance. Ready issues of later ones wait, listed in
  `status.waiting`, and the message names the release they wait for.
- Never held, or something waits for ever: a Standard change with its allowance,
  and an issue the current release's waiting issues depend on (transitively).
- An issue handed back to a person no longer holds its release.
- Running Tasks are never stopped. Without due dates nothing changes.

## Steps

1. `internal/model/crew.go`: `Release{Title, Due}`; `Issue.Release` and
   `CrewTask.Release` (nil when undated); `NextCrewStep` holds back later releases.
   Table tests first in `crew_test.go`.
2. `internal/gitprovider`: read the milestone's title and `due_on` into `Issue`;
   test with the fake Gitea.
3. `internal/controller/crew_controller.go`: read each active Task's issue (from
   the ready list, else `GetIssue`), so active Tasks know their release; envtest.
4. `api/v1alpha1/crew_types.go`: `waiting` doc says why issues wait;
   `make manifests generate`.
5. `docs/design.md` Crew bullet; `docs/results-v4.md` finding.
6. `make check`, review, `make deploy`; set due dates on the cel-lens milestones;
   watch the Crew list v0.2.0's issues as waiting for v0.1.1.

## Done when

`make check` passes, and in the Kind cluster the server Crew's status shows
v0.2.0's ready issues waiting while v0.1.1 has work, then starts them once it
has none.
