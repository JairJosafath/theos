# Plan: cel-lens with GitLens's DX, UI and UX, built by theos

The owner asked, on 9 November 2026, for cel-lens to be "as clean DX UI UX as
GitLens", built through theos and not by hand. cel-lens's Brief already
says "the way GitLens does for git", but nothing in the factory held the
work to that bar: the VS Code digest's UX section is the guidelines' minimum
(notifications, the status bar, command names), so the reviewer's
follow-ups, and the rounds that take them, went to README wording, test
helpers and the picture script's guards instead of to what a person sees.

## Goal

Every cel-lens pull request is built and reviewed against a written
GitLens-grade bar, and the factory has the work that brings cel-lens up to
it as input, without a person writing the code.

Done when ts-standard v1.8.0 is merged and its tag is seeded, and the
requests below are open on JairJosafath/cel-lens.

## Decisions

1. **The bar lives in the Standard, not in an issue.** One issue fixes one
   feature; a rule in `docs/style/vscode.md` holds every later change, the
   implementer reads it before building, and the reviewer checks it. It is a
   new section, *The UX bar: GitLens*, for `vscode-extension` only, in
   ts-standard v1.8.0. A Standard's files follow a bump of its `ref` through
   the pull request the operator opens (design.md, *Scaffold once*), so
   cel-lens-vscode gets it when the factory's catalog moves to v1.8.0, with
   no change to cel-lens by hand.
2. **GitLens is named as the reference, with its patterns as rules.** "Like
   GitLens" alone can't be reviewed. The section turns what GitLens does
   (blame at the line, CodeLens above the block, hovers as the details
   layer, a toggle for every annotation, views a click apart) into rules a
   reviewer can check, and takes the rest from VS Code's UX guidelines
   (views, sidebars, webviews, walkthroughs, quick picks, context menus,
   editor actions, the command palette), each source in `SOURCES.md` with
   the date it was read.
3. **The reviewer blocks only the hard rules.** A blocking finding must be
   a defect or a broken need. Four gaps are plainly that for an extension
   held to GitLens: something a person sees and can't turn off, a command
   missing from the Command Palette, a colour with no high-contrast default
   (unreadable for some people), a webview where a tree, an editor or a
   hover would do. Every other gap from the bar is a non-blocking finding,
   so the bar doesn't stall unrelated fixes.
4. **The follow-ups go to the bar first.** The reviewer names up to three
   follow-ups a pull request; for an extension they now go to where the
   extension, not only the change, falls short of the bar, before smaller
   polish. This steers the `status/proposed` stream, and so the Brief's
   later rounds, toward the experience.
5. **The features are requests on GitHub, not prompts.** This session can't
   touch the cluster, so it can't append to `spec.prompts`. The Brief reads
   the issues of the releases repository from allowed authors
   (plans/done/brief-requests.md); the owner's own account opens them. Each
   says what a person wants, not how; how is the planner's and the
   Crews'. If the Brief's entry has `requests.mode: Ask`, each waits in
   *Needs you* until its number is in `accepted`; if `Off`, the owner turns
   it on or pastes the same words as a prompt.
6. **What needs the owner's account stays out.** Publishing to the Visual
   Studio Marketplace or Open VSX is how GitLens installs in one click, but
   the publisher and its token are the owner's (GOVERNANCE.md, "Reserved to
   the owner"); no request asks the factory for it.

## The requests

Opened on JairJosafath/cel-lens, each under 4000 characters (the Brief keeps
no more):

1. Results on example objects, at the rule: the Brief's own promise, still
   under *Not supported yet*.
2. A cel-lens view: every CEL rule in the workspace and its state, a click
   from the rule.
3. Every cel-lens feature one step away and one command from off: toggles,
   menus, keybindings and a walkthrough.
4. Hovers as the details layer: what a rule means, its type, its cost
   against the API server's budget, and where to read more.
5. CEL in kubebuilder markers in Go files, where operator authors write it.
6. Inline info above every CEL rule, like GitLens's CodeLens, starting with
   giving a `dyn` value its type right there, kept in a committed
   `.cellens` file. The owner named this the core, after the first five
   were filed; it goes first.

## Steps

1. **ts-standard v1.8.0** (this change): the section, `SOURCES.md`, the
   pointers in `AGENTS.md` and the role guides, the pins in
   `templates/versions.txt`, the chart's catalog and the webshop sample, the
   template's README and docs/templates.md, and
   `TestVSCodeExtensionGuidesHoldTheUXBar`. Done when `make check` passes and
   a rendered vscode-extension passes `prettier --check`.
2. **The requests** (this session), opened on JairJosafath/cel-lens.
3. **The owner** merges step 1, upgrades the factory so the seed tags
   ts-standard v1.8.0, and lets the requests through if the Brief asks.
   The operator then opens the Standard update in cel-lens-vscode.

## Status, 9 November 2026, 04:40 UTC

The factory has done nothing on cel-lens since 4 November: the last
published source is `core v0.27.0` (03:54 UTC that day), and none of the six
requests has the operator's `planned` comment, which it posts on the round
that takes one. So either the Brief's `cel-lens` entry doesn't take requests
(`requests.mode` unset or `Off`), or the factory isn't running. This session
can't see the cluster to tell which. theos's `main` has moved since (the
rename to `theos.factory.io` on 4 November, v1alpha1 dropped on 6
November), so a factory installed before then needs the upgrade before it
does anything with this.

What the owner runs, in order:

```sh
# 1. Is it running, and what is the Brief doing?
kubectl -n cel-lens get brief cel-lens -o wide
kubectl -n cel-lens get brief cel-lens -o jsonpath='{.status.conditions}'

# 2. Let the GitHub requests through: find the entry for JairJosafath/cel-lens ...
kubectl -n cel-lens get brief cel-lens \
  -o jsonpath='{range .spec.releases[*]}{.container} {.github.repository} {.requests.mode}{"\n"}{end}'
# ... and turn requests on for it (N is its index, from 0). Only one entry
# per repository may take requests.
kubectl -n cel-lens patch brief cel-lens --type=json \
  -p '[{"op":"add","path":"/spec/releases/N/requests","value":{"mode":"On","authors":["JairJosafath"]}}]'

# Or, without requests, the core feature as a prompt:
kubectl -n cel-lens patch brief cel-lens --type=json -p '[{"op":"add","path":"/spec/prompts/-","value":"First, before anything else: inline info above every CEL rule, like GitLens CodeLens. A short greyed line above each rule and CEL variable, always there, each part clickable. The first use: give a dyn value its type inline. Clicking Give it a type lets the person pick a built-in kind, a CRD in the workspace, or write a struct right there ({name: string, replicas: int}) with completion and errors; then checks, hover and completion in that rule use it at once. What they gave is kept in a .cellens file in the repository, plain text, committed, reviewable, keyed by file, rule and path. One command and a setting turn the lines off. Details are JairJosafath/cel-lens issue 6."}]'
```

## Evidence

`docs/screenshots/gitlens-grade-ux.md`: the new test's output, Prettier on
the render, `make check`'s result and the requests' links.
