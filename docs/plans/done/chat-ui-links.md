# Plan: chat posts link to the UI's object pages

A follow-up of [chat-matrix.md](chat-matrix.md), whose step 3 left it for
later: it needed the UI's address as a setting. Written and built
2026-11-05.

## Goal

Each post `theos-chat` makes about a Task or a Container links to that
object's page in the UI, next to the Git host's links (pull request, issue,
repository, a deploy's address), so a person can go from a thread straight
to the page where they see the whole object and act on it.

## Decisions

- **The UI's address is a setting,** `theos-chat`'s `--ui-url`, the chart's
  `chat.uiURL`, as AGENTS.md's "What can be configured is configured" asks:
  where people reach the UI depends on how the factory is run. It is
  checked once at start (`model.ChatUIURL`): http or https, a host, no
  user, query or fragment (theos-chat adds the page's query), its trailing
  slashes dropped. A bad one stops `theos-chat` with the reason; the chart
  refuses it before then.
- **The default is the UI's own address,** found as the chart's default
  `sessionBinaryURL` finds it: `<ui.sessionBinaryScheme>://<ui.allowedHosts[0]>`
  behind an Ingress, else `http://localhost:<ui.service.port>`, the
  port-forward NOTES.txt prints. That link opens only on the computer running
  the port-forward, which the chart's README says, with how to set an address
  a phone reaches. With `ui.enabled` false and no `chat.uiURL`, no flag and
  no links: posts read as before.
- **A page is the UI's own link,** `<base>/?kind=<Kind>&name=<name>&ns=<namespace>`
  (`model.ChatPageURL`), what `ui/src/lib/hooks.ts` `parseUrl` reads: it opens
  the object's page in the drawer over the area the UI picks. The namespace
  is the Chat's, which is the object's.
- **Which posts.** A Task's (spec and triage threads, review verdicts,
  failures, merges) link to the Task's page, after its pull request and
  issue; a Container's (a failed scaffold's thread, releases, deploys
  waiting, deployed and failed) to the Container's, after the repository or
  the deploy's address. The link reads `Task in theos` or `Container in
  theos`. A Brief's news is only a digest line, which carries no links, so
  it has none; neither has the digest.
- **Open threads gain the link once.** A thread's root is edited when its
  text changes (`model.ChatHash`), so after an upgrade that sets the address
  each open root is edited once to carry it; done threads and past posts are
  left as they are.

## Steps

1. `internal/model`: `ChatUIURL`, `ChatPageURL`, and `PageURL` on
   `ChatTaskView` and `ChatContainerView`, added to the posts' links.
   `cmd/theos-chat`: `--ui-url`; `internal/chatbot`: `Bot.UIURL`, each view's
   page in the Chat's namespace. The chart: `chat.uiURL` and its default
   (`theos.chatUIURL`), `--ui-url` on theos-chat's Deployment.

## Done when

- `internal/model/chat_page_test.go`: the address's checks, a page's URL
  escaped, and a Task's and a Container's posts linking to their pages, and
  not without the address.
- `internal/chatbot/bot_test.go` `TestPostsLinkToTheUI`: an alert on the
  fake homeserver carries the Task's page in its body and as a link in its
  formatted body.
- `test/chart/chat_test.go` `TestChatChartLinksToTheUI`: the flag for the
  port-forward, an Ingress with and without TLS, `chat.uiURL`, and none
  without the UI; `TestChatChartRefuses` refuses a `chat.uiURL` without a
  scheme, with a query, or with a user.
- `make check` passes.
