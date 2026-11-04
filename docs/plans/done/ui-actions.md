# Plan: the UI takes most actions itself, as the person signed in

GitHub issue #706, asked for by the owner on 9 October 2026; the maintainer's
decisions are on the issue. Milestone *UI: act as the person signed in*.
Agent: otter-706.

## Goal

A person steers the factory from the UI: a Brief's settings and its *Needs
you* answers, a Crew's pause, parallel and release settings and a release
re-run, a Container's scaffold retry, a Task from a goal, and a Standard's
`ref` bump behind a confirmation. Over port-forward these work with no setup;
behind an Ingress they need a person signed in with OIDC, and the cluster's
RBAC decides what each person may do. design.md, *UI actions*, is the spec.

Done when the steps below are merged.

## Decisions

1. **Two modes, chosen by the chart; actions off by default.** The
   maintainer's decision 1 on #706. `ui.actions.enabled: false` keeps today's
   three Brief writes. Over port-forward (`ui.allowedHosts` empty) the UI's
   ServiceAccount makes the writes. Behind an Ingress, the chart `fail`s
   unless `ui.auth.oidc` is set, and every write impersonates the signed-in
   person. Why not a proxy header: anything else that reaches the pod can
   send it.
2. **Reads stay the ServiceAccount's.** Only writes impersonate. Reads behind
   an Ingress are what they are today; making them per person would need the
   person's list and watch on every kind, and a watch per person, for a page
   whose data is the same for everyone who may see it. A later issue can
   change this if an install needs it.
3. **The server picks the impersonated names, never the browser.** User
   `usernamePrefix` + the configured claim (default `oidc:` + `email`, which
   must be verified, `email_verified`), groups `groupsPrefix` + each of the
   token's `groups`. The prefixes keep a signed-in person from becoming a
   cluster user such as `system:admin` or a `system:` group. The
   ServiceAccount's `impersonate` grant can't be narrowed to prefixed names in
   RBAC, so the UI pod is as strong as the strongest user it may impersonate;
   design.md says so, and the chart grants `impersonate` only with
   `ui.auth.oidc`.
4. **No new dependency in the UI server.** `ui/server.mjs` runs from the
   image with no `node_modules`. OIDC is discovery, an authorization code
   with PKCE (`S256`), the token exchange, and an ID token checked with
   `node:crypto` (`createPublicKey` from the issuer's JWKS; RS256 and ES256
   only; `iss`, `aud`, `exp`, `nonce`). Tests run a fake issuer in
   `test/server.test.mjs`, as the fake API runs there today.
5. **Sessions live in the server's memory.** An opaque random id in an
   `HttpOnly`, `Secure`, `SameSite=Lax` cookie, the person's name and groups
   and the ID token's expiry; nothing else. One replica, so a restart signs
   everyone out; that is acceptable for a UI with one replica. The existing
   cross-site check (`isCrossSite`) still guards every write.
6. **One table of actions in `ui/server.mjs`.** Each entry is a method, a
   path pattern, a content type and a checker of the body: JSON patch ops on
   the listed paths only, or a merge patch touching only the listed
   annotation, or a create of the listed fields only. Today's `briefWrite`
   becomes three entries of that table. Anything not in it gets 403 with a
   message naming what the UI writes, and a 405 for a method no entry has.
   The body is checked, not its values: limits and enums are the CRD's, and
   the API server's refusal goes back to the browser as-is.
7. **`GET /ui/session`** tells the browser whether actions are on, the mode
   (`service-account` or `oidc`), and who is signed in, so a button can say
   "Sign in to act" instead of failing. It never returns a token.
8. **RBAC grants exactly the table.** `dist/chart/templates/ui/rbac.yaml`
   gets ClusterRole `<ui>-actions`: `create` on briefs and tasks, `patch` on
   briefs, crews, containers and standards, nothing else, rendered only with
   `ui.actions.enabled`. Today's `<ui>-brief-writer` stays for actions off.
   A Go test in `test/chart/` renders the chart in each mode and asserts the
   rules and bindings exactly: with OIDC the ServiceAccount has no
   `<ui>-actions` binding and has `impersonate`; `ui.auth.oidc.actionGroups`
   binds `<ui>-actions` to those groups (prefixed). `config/ui/rbac.yaml`
   keeps the actions-off grants.
9. **A *Needs you* answer is a prompt.** design.md already has the person
   answer suggestions and failed issues in a prompt, and the planner reads
   it. A one-click answer appends a prompt worded from the item (for example
   "Take suggestion #12 in orders-api: Add pagination." or "Retry issue #7 in
   orders-api."), so it needs no new write and no new planner rule, and the
   Brief's history shows what was said.
10. **A release re-run is a Crew annotation.** The UI never calls the Git
    host. `theos.factory.io/rerun-release` on a Crew, set or changed while
    `status.lastRelease.run` is `Failed`, makes the operator re-run that
    release's failed workflow runs through the GitProvider, once per value;
    the Crew's watch then sees the newest runs as it does when a person
    re-runs them in Gitea. The step checks what Gitea 1.27's API offers; if
    it has no re-run call, the operator deletes the tag and pushes it again
    at the same commit, which design.md already says the watch follows.
11. **The Standard bump's count comes from what the browser reads.** The
    dialog counts the Containers on the Standard (design.md, v4, *A Standard's fixes reach its Containers*), as
    the UI already reads Containers; the API decides the write.
12. **Buttons wait on #705's object pages.** The server, the chart and the
    operator steps don't; each button step says `Depends on` the page it
    lives on.

## Steps

Each step is one pull request that leaves main working, with `make check`
passing; screenshots go in `docs/screenshots/`. No step needs a cluster:
evidence is `node --test` against the fake API and a fake OIDC issuer,
Vitest, Playwright against the fake API (`ui/test/live.e2e.mjs`), envtest and
`make test-gitea`, and the Go chart tests.

1. **The UI server's action table and its RBAC, over port-forward** (#719).
   Decisions 1, 6, 7 and 8, port-forward mode: `ui.actions.enabled`, the
   chart's `fail` for actions with `allowedHosts` and no OIDC, the action
   table with every write in design.md's table, `GET /ui/session`, ClusterRole
   `<ui>-actions` and the Go chart test. No button yet; with actions off
   nothing changes. Done when `server.test.mjs` covers each entry and its
   refusals (other path, method, content type, field, op), the chart test
   asserts the grants in both settings, and a render with `allowedHosts` and
   no OIDC fails with a message naming `ui.auth.oidc`.
2. **A Crew re-runs its failed release run on request** (#720). Decision 10:
   the annotation, the GitProvider call, a Normal Event when the run hasn't
   failed. Done when envtest covers a failed run re-run once per value and a
   value set on a run that succeeded, and `make test-gitea` covers the
   GitProvider call against a local Gitea.
3. **Sign in with OIDC and act as the person** (#721, depends on #719).
   Decisions 2 to 5: `ui.auth.oidc` (`issuerURL`, `clientID`,
   `existingSecret`, `usernameClaim`, `usernamePrefix`, `groupsPrefix`,
   `actionGroups`), the login, callback and logout routes, the session, the
   impersonation headers on writes, the `impersonate` grant. Done when
   `server.test.mjs`, against a fake issuer, covers sign-in, a write carrying
   the prefixed impersonation headers, a write with no session refused, a bad
   signature, issuer, audience, nonce or expiry refused, and logout; the chart
   test covers the OIDC grants; `docs/getting-started.md` says how to turn it
   on.
4. **Brief actions on the Brief page** (#722, depends on #719 and #712). Its
   settings (autopilot, parallel, releases) and the one-click *Needs you*
   answers (decision 9). Done when Playwright against the fake API covers
   each button, a refusal shows the API server's message, and a signed-out
   page in OIDC mode says to sign in.
5. **Container retry and a Task from a goal on the Container page** (#724,
   depends on #719 and #712). The retry warns that the repository must be
   empty and links to it first. Done as step 4, for these buttons.
6. **Crew actions on the Crew page** (#725, depends on #719, #720 and #713).
   Pause and resume, parallel, the release settings, and the re-run button on
   a failed run. Done as step 4, for these buttons.
7. **Bump a Standard's `ref` behind a confirmation** (#726, depends on #719
   and #714). Decision 11. Done when Playwright covers the dialog with its
   count, cancel and confirm, and a refusal.

The last step to merge moves this plan to `docs/plans/done/` and updates
`ui/README.md` and design.md's UI text to match what ships.
