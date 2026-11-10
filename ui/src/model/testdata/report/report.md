# The factory's numbers: fixture

The Tasks that finished after 2026-10-06 12:00 UTC, up to 2026-11-05 12:00 UTC, counted from what each Task records. The same numbers as JSON: [fixture.json](fixture.json).

## What finished

| Outcome | Tasks |
| --- | ---: |
| Merged a pull request | 9 |
| Failed | 4 |
| Ended with no change needed | 1 |

The failed Tasks, by the reason each recorded:

| Reason | Tasks |
| --- | ---: |
| NoCIResult | 2 |
| BaseRed | 1 |
| Failed | 1 |

## Who wrote the code, and who let it in

| Merged Tasks | Tasks | Share |
| --- | ---: | ---: |
| With no commit by a person | 6 of 8 | 75% |
| On a person's approval | 2 of 9 | 22% |
| In their first round | 7 of 9 | 78% |

The first row is of the merged Tasks whose commits were read from the Git host.

## How long a merged Task took

From its first Implementing step:

| To | Tasks | Median | 90th percentile |
| --- | ---: | ---: | ---: |
| The pull request | 9 | 20m | 1d 1h |
| The first review | 9 | 25m | 1d 1h |
| The merge | 9 | 1h 30m | 1d 1h |

In each stage, over the Tasks that were in it:

| Stage | Tasks | Median | 90th percentile |
| --- | ---: | ---: | ---: |
| Queued | 8 | 2m | 5m |
| Waiting for a slot | 1 | 6m | 6m |
| Implementing | 9 | 20m | 2h 45m |
| CI checks | 9 | 6m | 1h |
| Reviewing | 9 | 10m | 55m |
| Verifying | 1 | 20m | 20m |
| Awaiting approval | 2 | 6h 30m | 10h |
| Blocked | 1 | 1d | 1d |

## What a merged Task cost

The median is $1.20, over the merged Tasks whose agents reported a cost: 7 of 9.

| Size, in changed lines | Merged | Reported a cost | Median cost |
| --- | ---: | ---: | ---: |
| S (under 100) | 4 | 4 | $0.65 |
| M (100 to 499) | 2 | 1 | $2.50 |
| L (500 to 999) | 1 | 1 | $9.00 |
| XL (1000 or more) | 1 | 1 | $20.00 |

1 merged Task had no size read and is in no row above.

| Harness and model | Merged | No commit by a person | Reported a cost | Median cost | Median time to merge |
| --- | ---: | ---: | ---: | ---: | ---: |
| claude-code · sonnet | 4 | 4 of 4 | 3 | $0.80 | 21m |
| claude-code · opus | 2 | 0 of 2 | 2 | $11.25 | 4h 7m |
| claude-code · default model | 1 | 1 of 1 | 1 | $9.00 | 1d 1h |
| not recorded | 2 | 1 of 1 | 1 | $0.50 | 5h 57m |

| Agent | Merged | No commit by a person | Reported a cost | Median cost | Median time to merge |
| --- | ---: | ---: | ---: | ---: | ---: |
| implementer | 5 | 5 of 5 | 4 | $1.00 | 22m |
| senior | 2 | 0 of 2 | 2 | $11.25 | 4h 7m |
| not recorded | 2 | 1 of 1 | 1 | $0.50 | 5h 57m |

## What the Scorers found

### docs/tone

1 Score on the criteria of generation 5. A rate is the passes out of the passes and fails; an unclear verdict is said apart.

| Criterion | All | claude-code · sonnet |
| --- | ---: | ---: |
| The page reads plainly \| no jargon | –, 1 unclear | –, 1 unclear |

### shop/quality

6 Scores on the criteria of generation 2. A rate is the passes out of the passes and fails; an unclear verdict is said apart.

| Criterion | All | claude-code · sonnet | claude-code · default model | claude-code · opus | not recorded |
| --- | ---: | ---: | ---: | ---: | ---: |
| The change does what the issue asks | 67% (4 of 6) | 67% (2 of 3) | 100% (1 of 1) | 100% (1 of 1) | 0% (0 of 1) |
| New behaviour has a test | 40% (2 of 5), 1 unclear | 33% (1 of 3) | 0% (0 of 1) | –, 1 unclear | 100% (1 of 1) |

## What these numbers don't show

- What people decided. Which issues were filed, what a spec says and when a release is deployed are people's choices. A Task a person approved still counts as merged with no person's commit when the person wrote none of its code: the share is of code, not of decisions.
- Why a Task failed. Failed Tasks are counted by the reason the Task recorded; what went wrong is on each Task's issue and pull request.
- What the failures cost. Cost is the median of the merged Tasks whose agents reported one. A failed Task's runs cost money too, and are in no number here.
- What is no longer recorded. The numbers come from the Tasks the cluster held when the report was made: a Task deleted before that is not counted, and a Task keeps the newest 64 steps of its timeline, so the times of a longer one are cut short.
- Other projects. This is one factory building one codebase, with its own Standards, Agents and issues. It says how that went, not how another project would go.
