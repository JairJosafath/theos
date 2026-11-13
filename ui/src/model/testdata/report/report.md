# The factory's numbers: report

The Tasks that finished after 2026-10-06 12:00 UTC, up to 2026-11-05 12:00 UTC, counted from what each Task records. The same numbers as JSON: [report.json](report.json).

## What finished

| Outcome | Tasks |
| --- | ---: |
| Merged a pull request | 14 |
| Failed | 4 |
| Ended with no change needed | 1 |

The JSON names each merged Task and its pull request, under `mergedTasks`, so a number here can be held against the pull requests it counts.

The failed Tasks, by the reason each recorded:

| Reason | Tasks |
| --- | ---: |
| NoCIResult | 2 |
| BaseRed | 1 |
| Failed | 1 |

## Who wrote the code, and who let it in

| Merged Tasks | Tasks | Share |
| --- | ---: | ---: |
| With no commit by a person | 10 of 12 | 83% |
| On a person's approval | 2 of 14 | 14% |
| In their first round | 11 of 14 | 79% |

The first row is of the merged Tasks whose commits were read from the Git host.

## How long a merged Task took

From its first Implementing step:

| To | Tasks | Median | 90th percentile |
| --- | ---: | ---: | ---: |
| The pull request | 14 | 22m 30s | 1h 30m |
| The first review | 14 | 30m | 1h 50m |
| The merge | 14 | 1h 22m | 10h 25m |

In each stage, over the Tasks that were in it:

| Stage | Tasks | Median | 90th percentile |
| --- | ---: | ---: | ---: |
| Queued | 12 | 2m | 5m |
| Waiting for a slot | 1 | 6m | 6m |
| Implementing | 14 | 22m 30s | 1h 40m |
| CI checks | 14 | 8m 30s | 1h |
| Reviewing | 14 | 7m 30s | 30m |
| Verifying | 1 | 20m | 20m |
| Awaiting approval | 2 | 6h 30m | 10h |
| Blocked | 2 | 16h | 1d |

## What a merged Task cost

The median is $1.01, over the merged Tasks whose agents reported a cost: 12 of 14.

| Size, in changed lines | Merged | Reported a cost | Median cost |
| --- | ---: | ---: | ---: |
| S (under 100) | 6 | 6 | $0.45 |
| M (100 to 499) | 4 | 3 | $2.50 |
| L (500 to 999) | 1 | 1 | $9.00 |
| XL (1000 or more) | 1 | 1 | $20.00 |

2 merged Tasks had no size read and are in no row above.

| Harness and model | Merged | No commit by a person | Reported a cost | Median cost | Median time to merge |
| --- | ---: | ---: | ---: | ---: | ---: |
| claude-code · sonnet | 6 | 5 of 5 | 5 | $0.80 | 23m |
| claude-code · opus | 3 | 1 of 3 | 3 | $3.00 | 3h 35m |
| claude-code · Opus | 1 | 1 of 1 | 1 | $1.13 | 42m |
| claude-code · default model | 1 | 1 of 1 | 1 | $9.00 | 1d 1h |
| claude-code · haiku | 1 | 1 of 1 | 1 | $0.40 | 32m |
| not recorded | 2 | 1 of 1 | 1 | $0.50 | 5h 57m |

| Agent | Merged | No commit by a person | Reported a cost | Median cost | Median time to merge |
| --- | ---: | ---: | ---: | ---: | ---: |
| implementer | 9 | 8 of 8 | 8 | $0.85 | 32m |
| senior | 3 | 1 of 3 | 3 | $3.00 | 3h 35m |
| not recorded | 2 | 1 of 1 | 1 | $0.50 | 5h 57m |

## What the Scorers found

### docs/tone

1 Score on the criteria of generation 5. A rate is the passes out of the passes and fails; an unclear verdict is said apart.

| Criterion | All | claude-code · sonnet |
| --- | ---: | ---: |
| Plain words \| no &lt;br> tags, no \`code\` and no \[link\](https://example.com) in a heading | –, 1 unclear | –, 1 unclear |

### shop/quality

7 Scores on the criteria of generation 2. A rate is the passes out of the passes and fails; an unclear verdict is said apart.

| Criterion | All | claude-code · sonnet | claude-code · Opus | claude-code · default model | claude-code · opus | not recorded |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| The change does what the issue asks | 71% (5 of 7) | 67% (2 of 3) | 100% (1 of 1) | 100% (1 of 1) | 100% (1 of 1) | 0% (0 of 1) |
| New behaviour has a test | 50% (3 of 6), 1 unclear | 33% (1 of 3) | 100% (1 of 1) | 0% (0 of 1) | –, 1 unclear | 100% (1 of 1) |

## What these numbers don't show

- What people decided. Which issues were filed, what a spec says and when a release is deployed are people's choices. A Task a person approved still counts as merged with no person's commit when the person wrote none of its code: the share is of code, not of decisions.
- Why a Task failed. Failed Tasks are counted by the reason the Task recorded; what went wrong is on each Task's issue and pull request.
- What the failures cost. Cost is the median of the merged Tasks whose agents reported one. A failed Task's runs cost money too, and are in no number here.
- What is no longer recorded. The numbers come from the Tasks the cluster held when the report was made: a Task deleted before that is not counted, and a Task keeps the newest 64 steps of its timeline, so the times of a longer one are cut short.
- Other projects. This is one factory building one codebase, with its own Standards, Agents and issues. It says how that went, not how another project would go.
