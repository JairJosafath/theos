# Plan: a try continues an earlier try's open pull request

GitHub issue #607, from #605 (reported by gray-lenovo). Agent: otter-607.

## Goal

A new try on an issue picks up where the last one stopped. When a Crew starts
another Task for an issue and an earlier try's pull request is still open, the
new Task works on that pull request's branch and reports to that pull request,
instead of starting a branch from main. A round that changes only the pull
request (its description) and commits nothing goes to the reviewer as it is;
when the reviewer approves, the Task merges it as usual.

Done when an envtest shows a Task for an issue with an open pull request from
an earlier try reusing that branch and pull request, and a round with no
commit that the faked reviewer approves ends Merged with the pull request
merged; `make check` passes.

## What happened

On `cel-lens-core#139`, try 2's code was approved in pull request #155; the
only open point was #155's description. Try 3 started a new branch from main,
so it could not finish #155: its agent rewrote #155's description, made no
commit, and the round could only end as "no change and no reason" or as
blocked. The issue failed its third try with its work done.

## Decisions, and why

1. **The earlier try is found in Kubernetes, then checked in Git.** The new
   Task lists the Tasks on its Container, keeps the other Failed ones on the
   same issue that recorded a pull request, newest first
   (`model.EarlierTry`), and reads that pull request: only an open one is
   continued. The Tasks already say which pull request was whose; listing the
   Git host's pull requests would need a new provider call and a guess at
   which belongs to the issue.
2. **Only the first run looks.** It happens when the Task's round 1 starts,
   before the sandbox: the branch, pull request and the earlier try's
   feedback go into status (`branch`, `pullRequest`, `pullRequestURL`,
   `feedback`, and the new `continues`, naming the earlier Task). Later runs
   keep the branch status names; a Task's own branch is still
   `task/<name>`.
3. **The earlier try's feedback comes along.** It says what the reviewer
   still wanted (on #155, the description), and the implementer and reviewer
   get it as round 1's feedback.
4. **No commit is a round, when there is a pull request.** The sandbox gets
   `THEOS_PULL_REQUEST` when the Task has one (a continued try, or any round
   after the first). The implementer is told so, and that a change to the
   pull request alone needs no commit. A run that commits nothing then
   reports the branch's head instead of `NoChangeNoReason`; the Task treats it
   as a pushed round (opening the pull request finds the open one), waits for
   the checks on that commit, and the reviewer decides. Without a pull
   request, a round with no commit still has nothing to review and keeps the
   "no reason" error.
5. **No status change on the earlier Task.** It stays Failed; its pull request
   is now the new Task's, which comments on it that it continues it.

## Steps

One pull request: the design change, `model.EarlierTry` with its table test,
the controller's adoption, `THEOS_PULL_REQUEST` in the sandbox and the runner,
the prompt line, and the envtest.
Evidence: [../../screenshots/607-try-continues-open-pr.txt](../../screenshots/607-try-continues-open-pr.txt).
