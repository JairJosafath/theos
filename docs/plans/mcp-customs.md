# MCP Customs: a real project, built by theos, for a weekend challenge

Written 2026-11-13 in a session the owner ran. Deadline: Monday
2026-11-16, 13:00 PT, the close of the AWS Builder Center weekend challenge
"No Pipeline, No Problem".

## Goal

theos has only ever built its own samples, so there is no feedback on how it
does on a project someone actually wants. This plan gives it one: a
real product, with a deadline and a judge, that has nothing to do with
theos. theos plans and builds it from a Brief; what goes wrong on the way is
the result we're after.

Two outcomes, in this order:

1. **The friction log** below: every place theos slowed, blocked or misled
   the build, each fixed on this branch with its test or filed as an issue.
2. **The entry**: MCP Customs deployed, a demo video under 3 minutes, an
   architecture diagram and a write-up, submitted before the deadline.

## The product

MCP Customs checks an MCP server before an AI agent may use it, and answers
pass, review or block with a report that says why. The Brief,
[config/samples/brief/mcp-customs.yaml](../../config/samples/brief/mcp-customs.yaml),
is the whole input theos gets; this section is what it means.

| Part | AWS | What it does |
| --- | --- | --- |
| Checker | Lambda durable function, Go SDK v1, provided.al2023, arm64 | Runs the fixed checks and the investigation loop durably |
| Sandbox | Lambda MicroVM | Runs the server under test with canary values and records its network and file activity |
| Judge | Amazon Bedrock, through a JSON Schema tool | Writes probe arguments, judges behaviour against purpose, picks the next investigation move |
| Store | DynamoDB, S3 | Checks, verdicts and tool-list snapshots; reports |
| Web | Go Lambda behind API Gateway, html/template | Submit, watch the run, read the report, Approve or Block |

Code decides everything that code can; the model only judges. This is the
"one move at a time" loop from
[Runtime-Planned Workflows on Lambda Durable Functions](https://claude.ai/artifact/Cvj9v5JXuReeW2pK5XokgS),
used only for the investigation, not to plan steps whose order is already
known. A static plan from a model was dropped on 2026-11-14: every branch
it chose (hash known, tool list changed, canary left) is a fact code
already has.

| | Code | Model |
| --- | --- | --- |
| Resolve, hash lookup, tool-list compare | ✓ | |
| Start the sandbox, list tools, run probes | ✓ | |
| Probe arguments from each tool's schema and description, canaries planted | | ✓ |
| Does the behaviour match the stated purpose; do descriptions instruct the model | | ✓ |
| Investigate, one registered action per turn, at most 8 turns | | ✓ |
| Verdict rules; the model may only make a verdict stricter | ✓ | |

Three submissions, one handler:

- **Seen before:** same hash and tool list as an approved check. Resolve,
  look up, compare, pass. No model call. Seconds.
- **New and fine:** a GitHub MCP server. The fixed checks, probes the model
  wrote, its tools call `api.github.com`; the model judges that matches the
  purpose. Pass, with no human.
- **New and hostile:** the bundled demo weather tool. Its plain probe only
  calls a weather API, so a fixed check would pass it. The model reads the
  description, which asks for the user's SSH key, probes again with a
  planted fake key, sees it posted to an unknown host, reads the source
  file that sent it, and asks a human, who blocks it.

An approved server is checked again later (a minute in the demo), and a
changed tool list is judged again and needs a new approval: the "rug pull"
case.

Choices made, and why:

- **us-east-2.** Durable functions launched there and MicroVMs launched
  there; the Go SDK runs on provided.al2023, which `aws-lambda-go` already
  uses.
- **Go for all application code**, the owner's choice: the Checker, the
  sandbox harness, the web page (server-rendered with html/template, no
  TypeScript app) and the demo server. Every Container starts from
  `aws-lambda-go` and the reference module in the doc above; only the CDK
  app stays TypeScript, as the template has it.
- **Readable by lint, not by review.** go-standard v1.7.0 and aws-standard
  v1.25.0 limit function size and complexity and file length (see
  [templates.md](../templates.md), go-standard), so the Crew's Go stays in small
  files and short functions. The chart pins those versions; they need
  their tags pushed before the install.
- **Go modules, npm, PyPI and remote URLs.** Container images inside a
  MicroVM are left out of the weekend.
- **Deployed by hand with `cdk deploy`.** theos can't deploy to AWS yet
  ([aws-deploy.md](aws-deploy.md)); for this challenge that fits its
  theme rather than hurting it.

## Steps

| When | Who | Step |
| --- | --- | --- |
| Fri | owner | Install the chart with `gitea.projects={mcp-customs}`, apply the Brief |
| Fri | theos | Planner writes the model, Crews and first issues |
| Fri | owner + session | Review the plan against this section; correct with `spec.prompts`, never by hand-editing the model. Log every correction |
| Sat | theos | Crews build the Checker, the sandbox harness, the web page and the demo server |
| Sat | owner | First `cdk deploy` to us-east-2; one real check end to end |
| Sun | theos + owner | The re-check path, the report page; record the demo |
| Mon AM | owner + session | Mirror the repositories to public GitHub, diagram, write-up, submit before 13:00 PT |

Rules for the weekend:

- **Fix only what blocks.** A theos fault that stops the build is fixed on
  this branch with a test that names it; anything else becomes a GitHub
  issue on theos with what happened, and the build goes on.
- **No hand-written product code in the Containers' repositories** unless
  theos can't get there by Sunday noon. Each such commit is logged, with why.
- **Agents never touch a cluster.** The owner runs the factory and the
  deploys; sessions write Briefs, prompts, fixes and the log.

## Done when

- Every row of the friction log has a fix commit or an issue number.
- The entry is submitted, with its demo video and diagram.
- `docs/results-mcp-customs.md` says what theos built alone, what needed
  a prompt, what needed a hand, and what that cost in time and tokens.

## Friction log

One row per thing that went wrong or needed a person. Newest last.

| # | When | What happened | Where in theos | Blocker? | Fix or issue |
| --- | --- | --- | --- | --- | --- |
| 1 | Fri | Known before starting: no AWS deploy | `aws-deploy.md`, not built | no (deploy by hand) | existing plan |
| 2 | Fri | Known before starting: no template knows durable functions or MicroVMs; the Crew must add both to a CDK app | `templates/aws-lambda-go` | to be seen | |
| 4 | Fri | No Standard limited Go function size, complexity or file length; agents' Go grows long | `templates/go-standard/.golangci.yml` | no | go-standard v1.7.0, aws-standard v1.25.0, aws-lambda-go v1.5.2 on this branch |
| 3 | Fri | Known before starting: the repositories live in the in-cluster Gitea; the entry needs them public | [github-host.md](github-host.md) | no (mirror by hand) | |
