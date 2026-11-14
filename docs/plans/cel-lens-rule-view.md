# Plan: every CEL rule of the workspace in one view, a click from the rule

Request: JairJosafath/cel-lens#2, the owner's, of 9 November 2026: "A
cel-lens view: every CEL rule in my workspace and how it's doing, a click
from the rule." Written on 13 November by the session the owner ran
("Continue work via theos on cel-lens"), after
[cel-lens-example-results.md](cel-lens-example-results.md) closed #1, from
JairJosafath/cel-lens `main` at f5bd74f (engine v0.31.0, extension v0.39.0).

## Goal

A person opens the cel-lens view in the side bar and sees every CEL rule of
the workspace, under the CustomResourceDefinition or policy it belongs to,
each with a label they recognise and its state: whether it compiles, has a
warning or an error, and whether its examples pass. A rule with a problem
stands out and is counted on the view's badge; one click opens the file at
the rule. Nothing has to be opened first to find out that a rule is broken.

Done when the request's own list holds on a released extension, and the
README shows the view with a picture.

## Where things stand

- **The engine knows a rule's state only for a file that is open.**
  Diagnostics, hover, completion and the line above each rule
  (`textDocument/codeLens`) are answered for open documents only. A closed
  file gets the problems the check of the examples finds (the schema's
  problems on an example, a wrong verdict's warning on a rule or an
  example, a CRD or policy the API server cannot read), never the
  problems of its own CEL. Nothing computes "this rule compiles" for a
  file nobody opened, but `cel-lens-core check` on the command line.
- **What the engine checks** (`pkg/check`, `document`):
  - a CustomResourceDefinition of `apiextensions.k8s.io/v1`: the `rule`
    and `messageExpression` of every `x-kubernetes-validations` entry, at
    any depth of each version's schema. `pkg/extract` also finds the rules
    of a `v1beta1` CRD, but the check does not check them;
  - a ValidatingAdmissionPolicy of `admissionregistration.k8s.io` (read
    into the v1 type): `validations` (`expression`, `messageExpression`),
    `matchConditions`, `variables` and `auditAnnotations`;
  - a MutatingAdmissionPolicy of `admissionregistration.k8s.io`:
    `matchConditions`, `variables` and each mutation's
    `applyConfiguration.expression` or `jsonPatch.expression`, compiled in
    the API server's admission environment. No example runs on it;
  - a Kyverno `ValidatingPolicy` of `policies.kyverno.io/v1`, only while
    the experimental feature `kyverno` is on: the same fields as a
    ValidatingAdmissionPolicy, in Kyverno v1.19.1's environment. No
    example runs on it. Kyverno's other kinds (`ClusterPolicy`, `Policy`,
    `MutatingPolicy` and the rest), another apiVersion of
    `ValidatingPolicy`, and an `evaluation.mode` other than `Kubernetes`
    or `JSON` are not checked.
- **Example results** exist for the rules of a CRD and for the
  validations, match conditions and audit annotations of a
  ValidatingAdmissionPolicy of `admissionregistration.k8s.io/v1` (a
  variable has no verdict of its own). The check of the examples runs in
  the background once typing pauses (250 ms), only while the setting
  `exampleResults` is on, and keeps per file, open or not, what each rule
  did to each example (`exampleFound[key].rules`, by document index, field
  path and text). `cel-lens.exampleResults` answers them per rule, naming
  each rule by `document`, `version`, `path` and `index` (a CRD's) or
  `document`, `field`, `name` and `index` (a policy's), with its
  `location`.
- **The workspace is walked by several background runs**, each with the
  same walk (`walkYAML`) and limits: at most 20,000 YAML files, none over
  1 MiB, hidden folders and `node_modules` skipped, nothing a symbolic link
  leads to outside the folder. The CRD scan runs only while
  `crd-typed-policies` is on and keeps only CRDs; the check of the examples
  runs only while `exampleResults` is on and reads only files with the
  words it looks for, again only when their size or time changed; the
  `.cellens` search and `typeChoices` have their own. None of them lists
  the rules of the workspace. `initialize` reports the limits under
  `capabilities.experimental.celLens` (`workspaceScanMaxFiles`,
  `workspaceScanMaxFileBytes`).
- **Workspace folders are read once**, from `initialize`. The engine does
  not declare `workspace.workspaceFolders`, so VS Code never sends
  `workspace/didChangeWorkspaceFolders`: a folder added later is walked by
  nothing until the engine restarts, and a folder removed is still walked.
- **Package `inline` holds every word the editor shows** of a rule: the
  line's `compiles` or `N problems` (`status`), the example part
  (`3 examples pass`, `refuses 1 example it should allow · 2 pass`), and the
  warnings, which cut a rule to one line of at most 60 characters. The
  location every part passes to `cel-lens.showRule` is `{uri, range}` of
  the expression.
- **The extension draws with no code of its own.** It runs engine
  commands through `ServerController.runEngineCommand`, registers the
  commands the line's parts run, and only hides the example part of a
  folder that turns `cel-lens.exampleResults` off (middleware). It has no
  view, no view container and no tree code; its stand-in `test/vscode.ts`
  has no `TreeView`. `docs/style/vscode.md` already names "every rule in
  the workspace" as what a view is for: one view container, a welcome
  view when empty, a click opens the thing, progress in the view.

## Decisions

1. **The engine makes the list; the extension draws it.** A new
   `workspace/executeCommand`, `cel-lens.ruleList`, answers the documents
   with rules of the workspace and each rule's identity, label, location
   and state; a notification, `cel-lens/ruleListChanged`, says when it
   changed, and the extension asks for what changed since the answer it
   has. The engine builds the list in a background run of its own, the
   *rule index*, which walks the workspace with `walkYAML` and its limits,
   reads only files with the words of a kind it lists, and checks each
   closed one with `check.Check`, the same options an open document gets
   (the Kubernetes version, the features, the CRDs known, the `.cellens`
   types). An open file is the editor's text and the check the message
   loop already made. So a rule's state in the view is the state its file
   would show if opened, from one checker.

   Rejected: the extension reading the YAML (a second opinion that drifts,
   and thousands of files parsed in the extension host, which the style
   guide forbids); `workspace/symbol` (a search, not a list with states);
   pull diagnostics (`workspace/diagnostic`) or publishing every closed
   file's diagnostics: per file, not per rule, and they would fill the
   Problems view with files the person did not open, which nobody asked
   for. Rejected too: running the index inside the check of the examples,
   which is off while `exampleResults` is off, and the view must not be.

2. **A rule is one entry of a list that holds CEL, named as
   `cel-lens.exampleResults` names it.** A CRD's rule is an
   `x-kubernetes-validations` entry: its `rule` and `messageExpression`
   are one row, whose problems are both's. A policy's rule is one item of
   `validations`, `matchConditions`, `variables` or `auditAnnotations`
   (a MutatingAdmissionPolicy's: `matchConditions`, `variables`,
   `mutations`). Its identity is its document and its place, without its
   text or line: `document` (`CustomResourceDefinition widgets.example.com`),
   then `version`, `path` and `index` for a CRD, or `field` and `index` for
   a policy; the answer gives it as one string `id`, prefixed with the
   file's URI. A second document of the same kind and name in one file
   gets `#2`. So typing in a rule keeps its row, and a rule added above
   does not move the others' rows to other documents.

   Rejected: the line or the text as identity (a row would vanish and come
   back on each keystroke, losing its selection), the document's index in
   the file (adding a document above renames every row below).

3. **A rule's label is the words its author wrote for people, else its
   CEL.** In this order: a static `message` (a CRD rule, a validation),
   the `name` (a match condition, a variable), the `key` (an audit
   annotation); otherwise the expression, on one line, cut past 60
   characters with `…`, as the warnings already cut a rule. A
   `messageExpression` is CEL, not words, so it never labels. The answer
   says where the label came from (`labelFrom`: `message`, `name`, `key`
   or `expression`), so a tooltip can show the expression when the label
   is a message. The words are written in package `inline`, which is the
   UI, with table tests.

   Rejected: the line's subject (`self: Widget.spec`), the same for every
   rule of one schema node; `reason` and `fieldPath`, which name a cause
   or a field, not the rule.

4. **Two states per rule, from what the engine already knows; the words
   are the line's.** The *compile state* comes from the check of the file
   alone: `error` when a diagnostic of severity error starts inside the
   rule's expressions, else `warning` for a warning, else `ok`, with the
   count and the first three messages. The warnings a wrong verdict puts
   on a rule are not in it, so a rule that compiles never says "1
   problem" for an example. The *example state* comes from the last check
   of the examples, as the line's part does: `wrong`, `error`, `pass`,
   `notRun`, or none (no example, a kind no example runs on, or
   `exampleResults` off), with the part's words exactly
   (`refuses 1 example it should allow · 2 pass`). The view's description
   of a rule is the line's own words (`compiles · 3 examples pass`,
   `2 problems`), so one idea has one word. A rule is a *problem* when its
   compile state is `error` or `warning`, or its examples are `wrong` or
   `error`; `notRun` is not a problem (`not run: needs an update example`
   says what is missing, not what is broken). The engine sets `problem` on
   each rule, so the filter and the badge count one thing.

5. **The view is a tree of folders, documents and rules.** One view
   container in the activity bar, *cel-lens*, with its own monochrome icon
   and one view, *Rules*. Under it: the workspace folders when there are
   more than one, in the workspace's order; under each, the documents with
   rules, in the order of their files' paths and then of the file; under
   each document, its rules in the order of the file. A document's label
   is its name (`widgets.example.com`), its description its kind and file
   (`CRD · config/crd/bases/widgets.yaml`), its icon its kind. A rule's
   icon says its state, worst first: an error, a warning (a compile
   warning, a wrong verdict, an example error), passing examples, or a
   plain check for a rule that compiles; colour is never the only sign,
   so the description says the same in words and each item sets
   `accessibilityInformation`. The tooltip has the label, where the rule
   is (`v1 · spec.ports[] · rule 0`), its CEL, its first problems and its
   example words. Clicking a rule opens its file with the start of the
   expression selected, in a preview editor as the Explorer does;
   right-click offers *Show Problems* (`cel-lens.showRule`) and *Show
   Example Results* (`cel-lens.showExampleResults`), the line's own
   commands. A document or rule row is never a button.

   Rejected: a webview (a tree shows it, and the style guide allows a
   webview only when nothing else can); a level per file (most files hold
   one document, and the guide asks for shallow nesting); a view inside
   the Explorer (the request asks for its own icon).

6. **"Follow the editor" selects the rule the cursor is in, and nothing
   more.** While the view is visible and following is on, 100 ms after
   the cursor last moved in a YAML editor, the view selects the rule whose
   entry holds the cursor (from the line of its list item to the end of
   the item: the engine gives this `span` beside the expression's
   `location`), else the document whose range holds it, else leaves the
   selection. It reveals the row, expanding its document, but never takes
   the focus, never opens the view, and never selects a row the filter
   hides (it selects the document then, when shown). Following is the
   setting `cel-lens.ruleView.followEditor` (default `true`), flipped by
   *Follow Editor* and *Stop Following* in the view's title menu.

   Rejected: following by moving the focus to the view (it would steal
   typing); following only the active file without the cursor (the
   request asks for the rule I am in).

7. **The filter and the badge.** *Show Problems Only* and *Show All Rules*
   in the view's title bar switch the view to the rules with `problem`,
   with their documents and folders, and back; the choice is kept per
   workspace (`workspaceState`), as a view's state, not a setting. The
   view's badge is the number of rules with a problem, whatever the
   filter, with the tooltip `3 rules with problems`, and no badge at 0.
   The badge counts rules, not problems, so it matches the rows the
   filter shows.

8. **Never an empty pane.** When the tree has no row, a welcome view
   (`viewsWelcome`) says why in one sentence and links to the step that
   helps, chosen by a context key the extension sets:
   - no folder open: *Open a folder to list the CEL rules in it*, with
     *Open Folder*;
   - the engine not running: *The cel-lens engine is not running*, with
     *Show Logs* and *Restart Engine*;
   - scanning: *Looking for CEL rules in the workspace…*;
   - nothing found: *No CEL rules in this workspace yet*, the kinds
     cel-lens lists, and links to the README's section and, when `kyverno`
     is off, to the setting that turns it on;
   - the filter on and no problem: *No rule has a problem*, with *Show All
     Rules*.

   While a scan runs (the first, or after the Kubernetes version, the
   features or the folders change) the view shows VS Code's progress bar
   (`window.withProgress` with the view's location) and its message says
   how far it is (`Scanning: 4,210 YAML files read, 37 with rules`). Files
   the limits left out are the first row, a warning: *1,203 YAML files not
   read: the scan reads at most 20,000 files of at most 1 MiB*, which
   shows the logs when clicked.

9. **It stays quick by doing only what changed, off the message loop.**
   - The index keeps, per file, what it read (by size and time, as the
     check of the examples does), the documents and rules it found and
     the check it made, and checks a file again only when its text, the
     Kubernetes version, the features, the CRDs it was typed from or the
     `.cellens` types it used changed. A change waits for typing to pause
     (250 ms), as the check of the examples does.
   - It runs one file at a time and waits while the message loop handles a
     message (the `busyGate` the check of the examples uses), so hover,
     completion and the lines wait for nothing. Its checks of CRDs share
     the Kubernetes-version guard the other checks share.
   - Each answer has a `generation`. `cel-lens.ruleList` with
     `{"since": n}` answers only the files that changed after generation
     n and the URIs `removed`; an older or unknown `n` (a restarted
     engine) gets the whole list with `"full": true`. The notification
     carries the generation and the scan's progress, never the list, and
     is sent at most every 500 ms during a scan, once at its end, and
     after each change.
   - The extension keeps the list in a model without `vscode` (plain
     TypeScript, tested in Node), applies each answer file by file, and
     fires a tree change only for the documents that changed.
   - The limits are the scan's: 20,000 YAML files, 1 MiB each, only files
     with the word of a kind listed parsed. The example states have the
     example check's own cap (100 examples of a CRD or policy). There is
     no cap on rules; benches measure 5,000.
   - The index runs from `initialize` for a client that asks for the list
     (`initializationOptions.ruleList: true`), whether or not the view is
     open, since the badge counts problems while the view is closed;
     another client pays nothing until it runs the command.

   Rejected: the whole list in every notification (megabytes per
   keystroke in a big repository); indexing only while the view is
   visible (the badge would be wrong exactly when the view is closed).

10. **The kinds listed are the kinds the engine checks, and no more.** A
    CRD of `apiextensions.k8s.io/v1`, a ValidatingAdmissionPolicy and a
    MutatingAdmissionPolicy of `admissionregistration.k8s.io`, and, only
    while `kyverno` is on, a Kyverno `ValidatingPolicy` of
    `policies.kyverno.io/v1` in an evaluation mode the engine checks.
    Turning `kyverno` off removes those rows; on, they come. A
    MutatingAdmissionPolicy, a Kyverno policy, a ValidatingAdmissionPolicy
    of another version than v1 and a policy's variables have a compile
    state and no example state, and their tooltip says example results do
    not cover them. A `v1beta1` CRD, Kyverno's other kinds and Helm
    templates are not listed: the engine does not check their CEL, so a
    row would say "compiles" for CEL nobody compiled.

11. **Several workspace folders are several roots, followed as they
    change.** A file belongs to the deepest workspace folder that holds
    it, as VS Code's `getWorkspaceFolder` does; an open file in no folder
    is listed under *Open files*, last, while it is open. The engine
    declares `workspace.workspaceFolders` with `changeNotifications` and
    follows `workspace/didChangeWorkspaceFolders`: the rule index, the
    check of the examples, the `.cellens` search and the CRD scan read a
    folder added and forget one removed, without a restart. A folder
    whose `cel-lens.exampleResults` is off shows no example words in the
    view, as its lines show none (the engine has one switch per window:
    cel-lens#18).

    Rejected: one flat list with the folder in the description (two
    folders with the same layout look the same); restarting the engine on
    a folder change (a full rescan, and every open editor's lines blink).

12. **Release: the engine, then the extension that pins it, with the
    picture.** The engine's steps are released as v0.33.0 (*Prepare
    v0.33.0*, then `release.yml` for the engine); the extension pins it in
    `core-version.json`, its e2e test asks the pinned engine for the list,
    the README gains a *Rule view* section with a picture taken by
    `npm run pictures` (`images/rule-view.png`, as #1's pictures were taken
    by cel-lens-vscode#15), and it is released as v0.41.0 with
    `release.yml`. The version numbers are the next minors unless another
    release comes first.

## Steps

Each is an issue in the session's Gitea (bodies in
[cloud-session/issues/](cloud-session/issues/)), stacked in this order.

| Step | Repository | What |
| --- | --- | --- |
| 1 | cel-lens-core | The rule index and `cel-lens.ruleList`: the documents with rules of the workspace, each rule's identity, label, location, span and compile state, closed files checked off the loop with the scan's limits (`w1.md`) |
| 2 | cel-lens-core | Keeping a client current: generations, `since` and `removed`, the `cel-lens/ruleListChanged` notification for a client that asks, the scan's progress and files left out, every change followed (`w2.md`) |
| 3 | cel-lens-core | Example states in the list, with the line's words; `problem` on each rule; a notification when the check of the examples changes them (`w3.md`) |
| 4 | cel-lens-core | Workspace folders added and removed are followed by every background run, without a restart (`w4.md`) |
| 5 | cel-lens-vscode | The view: its container and icon, the tree from the list, labels, states, tooltips, the click and the context menu, folders, welcome views, progress (`w5.md`) |
| 6 | cel-lens-vscode | The problems-only filter, the badge, and following the editor (`w6.md`) |
| 7 | cel-lens-core | Prepare v0.33.0; released with `release.yml` (`w7.md`) |
| 8 | cel-lens-vscode | Pin engine v0.33.0; the README's section and picture; Prepare v0.41.0, released with `release.yml` (`w8.md`) |

## Status

- **13 November, 19:30 UTC.** Planned. Nothing built.
- **13 November, evening. Steps 1 and 4 approved.** cel-lens-core#41
  (issue #35, the rule index, with #40 for #38 merged in) is approved
  after four review rounds. The reviews found that a rule the check never
  compiled was listed `ok`: a document the API server cannot read, a
  schema that is not structural or whose defaults it refuses. `pkg/check`
  now says which rules it did not compile (`Diagnostic.Uncompiled`),
  deciding a whole version as apiextensions-apiserver does, and the list
  shows those rules as errors. One fix had made every check of a CRD
  with defaults 50% slower; a bench with 900 defaults now holds it.
  Decision: the index's cost while it runs (+1 to 2 ms on a 4 ms hover,
  re-measured alone) is accepted, as the check of the examples' larger
  one is. Follow-ups: #42 (the line above a rule says `compiles` for
  such a rule) and #43 (the reviews' non-blocking items). Building now:
  #36 (engine) and cel-lens-vscode#24 (the view, against #41's engine).
- **14 November, 04:00 UTC. All engine steps approved; renumbered.** The
  owner's request to make `.cellens` types experimental shipped first, as
  engine v0.32.0 and the extension's v0.40.0, so this plan's release
  steps move to engine v0.33.0 (cel-lens-core#39) and the extension's
  v0.41.0 (cel-lens-vscode#26). Approved: #45 (issue #36, the list kept
  current; a file that left, came back and left again was missing from
  `removed`, fixed by naming every file that left), #47 (issue #37,
  example states in the line's own words), #48 (v0.32.0 merged in; the
  index takes `.cellens` types only through the one gate), #49 (issue
  #42, the line says `not checked` for a rule never compiled) and #50
  (Prepare v0.33.0). In the extension: #27 and #30 (issues #24 and #25,
  the view, its filter, badge and following the editor, two rounds each)
  and #32 (v0.40.0 merged in; 23 of 23 end-to-end cases pass on the
  v0.33.0 candidate).
- **14 November, 04:30 UTC. Done.** Engine v0.33.0 (JairJosafath/cel-lens#31,
  merged at b2e4235) and the extension's v0.41.0 (#32, merged at 3b3af71;
  cel-lens-vscode#33, issue #26, one review round, its changelog wording
  and a notification wait that proved nothing fixed before publishing)
  were published by `release.yml`. Checked here: both releases'
  signatures and provenance verify with cosign; the engine's `initialize`
  offers `cel-lens.ruleList`; the Linux `.vsix` carries engine v0.33.0
  with the pinned hash and contributes the `cel-lens.rules` view; the
  README's `rule-view.png` answers on `main`. JairJosafath/cel-lens#2 is
  closed, with a comment going through its list. Open follow-ups:
  cel-lens-core#43 (the reviews' smaller items, and the engine sending a
  document's `#n` so the view stops counting), cel-lens-vscode#28 (retry
  a cut-off engine download). Not tried: the full `npm run pictures`
  with every picture, light and high-contrast themes.
