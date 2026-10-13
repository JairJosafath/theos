# Plan: a prompt answers any waiting suggestion, and a round says which it couldn't

GitHub issue #1019, reported from a device running 0.22.0 (Brief `cel-lens`,
62 suggestions waiting). Agent: otter-1019.

## Goal

A prompt can answer any reviewer's suggestion waiting in the Brief's
repositories, however many wait, and the planner can tell two repositories'
suggestions with the same number apart. When a prompt names a suggestion that
doesn't wait for an answer, the round says so in words the operator wrote,
instead of the planner claiming it is handled.

Done when the steps below are merged, and design.md, *The person answers the
reviewers' suggestions in a prompt*, says what they built (this plan's pull
request writes that text ahead of the code).

## What happens today

- `model.MaxSuggestions` (50) caps `status.suggestions`. The workspace's
  `model/suggestions.yaml` is rendered from that status list
  (`prepareWorkspace` sets `ws.Suggestions = waitingSuggestions(brief)`), and
  `CheckAnswers` checks a plan's answers against the same list. Sorted by
  Container and number, the last repository's newest suggestions fall off,
  and an answer to one is refused as "not a waiting suggestion".
- `model/suggestions.yaml` names a suggestion by `container` and `issue`
  only; a prompt says "cel-lens-vscode #385". The engine's #364 and #371 and
  the extension's #364 and #371 sit side by side, told apart only by the
  Container name.
- A plan has no way to say a prompt named a suggestion it can't answer, so the
  planner wrote it into `plan/choices.md`, untrue ("already open issues"), and
  the round reported `Applied`.

## Decisions

1. **The status list is for the card; the workspace lists them all.**
   `status.suggestions` keeps at most 50 (`MaxSuggestions`): it is in the
   Brief object, read by the UI and every watch, and `suggestionsWaiting`
   already counts the rest (the card says how many wait). The workspace is a
   Git file, so `model/suggestions.yaml` lists every waiting suggestion the
   reconcile found, up to `MaxWorkspaceSuggestions` = 500, rendered from what
   `gatherWorkspace` read, not from the status. A repository that couldn't be
   read adds the ones the status last listed for it, as today. Past 500,
   `brief.md` says how many more wait unlisted and that the plan may still
   answer one a prompt names, by Container and number.
   *Why:* the cap on the status exists for the object's size; nothing about
   the workspace needs it, and the planner can only answer what it can see.
2. **Answers are checked against the Git host, not the status list.** An
   answer to a suggestion the status lists is checked as today. One it
   doesn't list is read from the Git host (`GetIssue` on the Container's
   repository) when the plan is checked: it is waiting when it is open and
   `model.IsSuggestion`, and its criteria come from its body. The controller
   builds that list and `model.CheckAnswers` stays pure. A plan answers at
   most `MaxSuggestionAnswers` = 100 suggestions, which bounds the reads.
   *Why:* whatever bound the workspace has, an answer the operator can verify
   is never refused for a list's length.
3. **A suggestion is its repository and number.** Each entry of
   `model/suggestions.yaml` gains `repository` (the repository's name on the
   Git host, as a prompt would write it) and `url`. `brief.md`'s *Reviewer
   suggestions* section says a suggestion is its Container and number
   together, that the same number in two Containers is two suggestions, and
   to match a prompt's repository name or address to an entry's `repository`
   or `url`. A refusal for an answer that waits in no Container names the
   Containers where that number does wait, so a retry can fix the mix-up.
4. **A prompt-named suggestion that doesn't wait is answered `missing`.**
   `plan/suggestions.yaml` gains a third answer, `missing`, with a why: a
   suggestion a new prompt names that `model/suggestions.yaml` doesn't list.
   The operator reads it from the Git host when it checks the plan and refuses
   `missing` for one that does wait ("cel-lens-vscode#385 waits for an
   answer: take or skip it"). Applying the round, it does nothing on the Git
   host and records each `missing` answer on the round, in its own words with
   what it found ("A prompt named cel-lens-vscode#385, which no longer waits
   for an answer: it is labelled status/ready" / "is closed" / "doesn't exist"
   / "isn't a reviewer's suggestion"), in a new `status.rounds[].unanswered`
   list (at most 20, 500 bytes each) the round's card shows under its
   summary. `PLANNER.md` and `brief.md` say: never write in `choices.md` that
   a named suggestion is handled; answer it.
   *Why the round and not Needs you:* nothing waits on the person; they read
   the round to see what it did, and a new prompt fixes it. *Why `missing`
   and not a refusal:* the planner is right not to invent an answer, and a
   refusal would loop a round over a suggestion that is gone.

## Steps

Each is its own issue, `status/proposed`, naming #1019.

- **S1 (#1022). The planner sees and can answer every waiting suggestion**
  (decisions 1 to 3). `model/suggestions.yaml` from what the reconcile read,
  up to 500, with `repository` and `url`; `brief.md` text; answers past the
  status list checked against the Git host; at most 100 answers; the refusal
  naming the Containers where the number waits. Done when a table test renders
  62 suggestions across two Containers into the workspace with both
  Containers' entries, an envtest takes the cel-lens case (62 waiting, engine
  and extension, a plan taking the extension's six newest, #385 #376 #371
  #369 #365 #364, which `status.suggestions` doesn't list) through the check
  and apply, and the six are labelled `status/ready` on the fake Git host
  while the engine's #364 and #371 are untouched; and `make check` passes.
- **S2 (#1023). A round says which named suggestions it couldn't answer**
  (decision 4). The `missing` answer, its check against the Git host,
  `status.rounds[].unanswered`, the round card, `PLANNER.md` and `brief.md`.
  Done when model tests cover `missing` (with a why, never with `fix`), an
  envtest shows a plan answering `missing` for a closed suggestion applies
  with the operator's line in `unanswered`, and one answering `missing` for a
  waiting one is refused naming it; the UI test shows the line on the round
  card; and `make check` passes. Depends on S1 (both change `CheckAnswers`).

## Not in this plan

Which 50 the status lists stays as it is (by Container and number): with
answers no longer tied to the list, its order only changes what the card shows.
