# Plan: hovers that tell me what a rule means, what it costs and where to read more

## Request

JairJosafath/cel-lens#4, the owner's: "Hovers that tell me what a rule
means, what it costs and where to read more." Written on 14 November from
JairJosafath/cel-lens `main` at 3b3af71 (engine v0.33.0, extension
v0.41.0), while the extension is being built for cel-lens#3
([cel-lens-everything-one-step-away.md](cel-lens-everything-one-step-away.md),
v0.42.0, no engine change), and with cel-lens-core#51 (issue #43, approved,
branch `claude/43-index-leftovers`) waiting for the next engine release.

The request, in its words:

> GitLens shows the details layer in its hover: the annotation stays short,
> and hovering it gives everything else in a fixed, readable layout with
> links to act on. cel-lens's hover gives the type and description of a
> field. I want hovering a CEL rule, or any part of it, to tell me what I
> need to trust it.
>
> - Hovering a rule's expression shows, always in the same order:
>   - what it checks, in a plain sentence (for example "every port is below
>     65536");
>   - the variables it uses (`self`, `oldSelf`, `object`, `params`, ...) and
>     their types, saying when one is `dyn` and why;
>   - its estimated cost against the API server's budget for the Kubernetes
>     version I picked, so I see a rule near the limit before the API server
>     refuses it, with the fix when there is one;
>   - a link to the Kubernetes docs for that kind of rule, and to the CEL
>     function's docs when I hover a function.
> - Hovering a part of the expression shows that part's type, the way it
>   does today, in the same layout.
> - Hovering a problem shows the API server's message and, when there's a
>   fix, a link that applies it.
> - Expressions in the hover are coloured like the code.
> - Links in hovers only run cel-lens's own commands.
>
> Before I commit a rule I want to know it means what I think and that the
> API server will take it. Today I find out about cost when I apply the CRD.

## Goal

Hovering anywhere in a CEL expression cel-lens checks shows one hover, its
sections always in the same order: the part under the cursor when it is on
one, the fix for a problem under the cursor, what the rule checks in a
sentence that is right whenever it is shown, the variables it reads with
their types and why one is `dyn`, its estimated cost against the API
server's budgets for the Kubernetes version picked (with a fix when a limit
would help), what it does to its examples, and links to the pinned docs of
that kind of rule and of the function hovered. CEL in the hover is
coloured as cel-lens colours it in the editor, and a link in the hover
can run only the cel-lens commands the hover needs.

Done when the request's list holds on a released engine and extension, the
README shows the hover with a picture, and the theos evidence is saved in
`docs/screenshots/cel-lens-rule-hovers/`.

## Where things stand

From `engine/` and `vscode/` at 3b3af71.

- **What the hover shows** (`session.hover` in `internal/lsp/lsp.go`,
  `Assistant.Hover` in `internal/assist/assist.go`):
  - on a variable: a ` ```cel ` block `(variable) self: object` (`object:
    apps/v1 Deployment`, `params: dyn`, `request:
    kubernetes.AdmissionRequest`), its description, and notes for a type
    `.cellens` assumes (`withAssumed`) or would give with `cellens-types`
    off (`withCellensOff`);
  - on a field after a dot: `(field) name: type`, the schema's
    `description` and the kinds it takes;
  - on a function or macro: up to six overloads with the one the call
    selects marked `// this call`, a description, an example in a ` ```cel `
    block (`[1, 2, 3].all(x, x > 0) // -> true`), and one docs link. In a
    Kyverno policy a function gets no docs link (`a.vars != nil`);
  - why a value is `dyn` (`withDynReason`, from a fixed list of thirteen
    reasons in `internal/assist/dyn.go`) only while the experimental feature
    `fitting-completion` is on;
  - on a CRD rule with example results, the section `inline.ExamplesHover`
    (`**Examples:** 3 examples pass`, each example linked to its file and
    line) below a `---`, or alone where the cursor is on no name;
  - anywhere else in an expression (an operator, a literal, a string, a
    space) nothing, and outside CEL nothing. The range is the name hovered.
  - A schema's `description` goes into the hover as it is written, not
    escaped. The examples section escapes its text (`inline.escape`).
- **Clicking the line above a rule** runs `cel-lens.showRule`; with no
  problem the extension opens the hover at the rule's start
  (`editor.action.showHover`, `rule_parts.ts`), so the hover is already
  where the line sends a person for the details.
- **Cost is computed only to explain a CRD cost error.** `pkg/check`
  runs the API server's validation of a CRD
  (`validation.ValidateCustomResourceDefinition` under the version guard
  `UnderVersion`), which compiles every rule with `celschema.Compile`, the
  version's `EnvSet` and `celconfig.PerCallLimit`, multiplies each cost by
  the node's cardinality, and refuses a rule or `messageExpression` over
  `StaticEstimatedCostLimit` (10,000,000) and a version's schema whose
  rules together pass `StaticEstimatedCRDCostLimit` (100,000,000)
  (`k8s.io/apiextensions-apiserver` v0.37.2, the one library the engine
  builds, for every version from 1.33 to 1.37). Only when it does,
  `pkg/check/cost.go` estimates the costs again with the same compiler
  (`collectRules`, `nodeRules`, the `TotalCost` accumulator), names what
  makes the rule expensive (`drivers`: an unbounded list it runs for each
  element of, an unbounded string, list or map it reads, `string()` of a
  number), and sizes limits (`sizer`, `size.go`: the largest of 1, 2, 5,
  10 … 10M that brings it under its budget, preferring one limit of 100 or
  more). With an unbounded field near a rule (`mayCostMuch`) the estimate
  starts in the background while the validation runs (`warm`). Nothing
  ever says what a rule costs while it is under its budget.
- **Policies get no cost.** The API server's validation of a
  ValidatingAdmissionPolicy or MutatingAdmissionPolicy only compiles each
  expression (`plugincel.CompileCELExpression` adds
  `environment.StrictCostOpt` and estimates nothing); policies have run-time
  limits only (`PerCallLimit` 1,000,000 per evaluation, in the base
  environment; `RuntimeCELCostBudget` 10,000,000 per binding).
  `TestPolicyNoCost` holds that the engine reports none. Kyverno v1.19.1's
  ValidatingPolicy compiler builds on the same base environment and
  estimates no cost either (no estimator call in `kyverno` or its `sdk`).
- **The quick fixes** (`internal/lsp/codeaction.go`): *Change to `name`*
  for a misspelled name, and *Add `maxItems: 100` to `spec.ports`* (or
  `maxLength`, `maxProperties`, or one action for limits that only help
  together) for a cost error, from `check.Diagnostic.Fixes`. Code actions
  are offered only for a diagnostic; the diagnostic's `data.fixes` carries
  the limits.
- **Links in the hover today:**
  - `https://github.com/google/cel-spec/blob/master/doc/langdef.md` (and
    four of its headings): a branch, not pinned;
  - `https://kubernetes.io/docs/reference/using-api/cel/` (and its list and
    quantity library headings): the current release's docs, whatever
    version is picked; most Kubernetes functions get the page without a
    heading;
  - `https://pkg.go.dev/github.com/google/cel-go/cel#hdr-…-OptionalTypes`:
    the newest cel-go, not the v0.30.0 the engine builds;
  - the examples' links to their files (`file:` URIs).

  None goes to the Kubernetes page for the kind of rule hovered, and
  there are no command links.
- **The extension shows the engine's hover as it comes.** It has no hover
  middleware, sets no `isTrusted` (so a `command:` link would not run) and
  no `supportHtml`. It contributes no `cel` language or grammar, so a
  ` ```cel ` block in a hover is plain monospace text. The editor's CEL
  colours are its own decorations (`ColorPainter`, the lexer in
  `cel_lexer.ts`, the palettes of `cel_styles.ts` per colour style and
  theme kind), not a TextMate grammar. The language client's own
  registration of the engine's commands is turned off
  (`CelLensClient.registerFeature`); the extension runs them with
  `runEngineCommand`.
- **VS Code's hover** (1.100, `markdownRenderer.ts`, read on 14 November):
  a fenced block is coloured only by a registered language's tokenizer;
  with `supportHtml` the sanitizer keeps `pre`, `code` and `span`, and on a
  `span` only a `style` of the form `color:#hex;` (or a theme variable);
  a `command:` link runs only when the string is trusted, and with
  `isTrusted: {enabledCommands}` only for those ids. When the cursor is on
  a diagnostic, VS Code shows its message and *Quick Fix...* in the same
  hover, above the providers' content.
- **The style guides already ask for this.** `vscode/docs/style/vscode.md`:
  "Hovers are the details layer: one fixed order of sections (what it is,
  its type or result, why, a link to the docs), Markdown rendered like the
  code, and command links only to the extension's own commands
  (`MarkdownString.isTrusted = { enabledCommands: [...] }`, never
  `true`)."
- **Benches:** `BenchmarkHover` (a hover on an unchanged 1,000-line CRD
  through the message loop), `BenchmarkDiagnostics` (300 ms budget),
  `BenchmarkCostFixes` and `BenchmarkCheckCostErrors` in `pkg/check`.

## Decisions

1. **One hover, one order, anywhere in an expression.** Every position
   inside a CEL expression the engine checks (a CRD's `rule` and
   `messageExpression`; a policy's validation, `messageExpression`, match
   condition, variable, audit annotation `valueExpression` and mutation; a
   Kyverno ValidatingPolicy's while `kyverno` is on) gets the hover,
   strings and operators included. Its blocks, separated by `---`, always
   in this order, each left out when it has nothing:

   1. **The part**: what the cursor is on (a variable, field or function),
      exactly as today: the ` ```cel ` declaration or overloads, the
      description, the example. Left out where the cursor is on no name.
   2. **Fix**: the problems whose range holds the cursor, each with its fix
      links (decision 7).
   3. The rule, as labelled paragraphs:
      - **Checks:** (or **Returns:** for an expression that is no check):
        decision 3;
      - **Uses:** decision 4;
      - **Cost:** decisions 5 and 6;
      - **Examples:** the section the engine already writes, unchanged;
      - **Docs:** decision 8.

   The range is the name on a part, so the hover stays while the mouse
   moves along it, and the expression elsewhere. Positions outside CEL keep
   getting no hover; the YAML key (`rule:`) gets none, since the line above
   the rule already opens this hover. The hover is not a setting: cel-lens#3
   settled that what shows only when asked needs no toggle.

   A sketch (numbers made up):

   > ```cel
   > (field) port: integer
   > ```
   > The port to listen on.
   >
   > ---
   >
   > **Checks:** for every `p` in `self.ports`, `p.port` is less than `65536`.
   >
   > **Uses:** `self`: `Widget.spec` (object), the value the rule is on.
   >
   > **Cost:** estimated 6.2M of the per-expression budget of 10M (62%), half
   > or more: doubling a limit it depends on can take it over. All rules of
   > `v1`'s schema: 6.4M of 100M (6%). [Kubernetes 1.37](command:cel-lens.pickKubernetesVersion "Pick the Kubernetes version")
   > Why: `spec.ports` is a list with no `maxItems`, and the estimator
   > assumes up to 3.3M items. [Add `maxItems: 1000` to `spec.ports`](command:cel-lens.applyFix)
   >
   > **Examples:** 3 examples pass
   >
   > **Docs:** [Validation rules](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules) (Kubernetes 1.37)

   Rejected: the part alone on a name and the rule only off one (most of a
   rule is names, so the rule's sections would be hard to reach; the
   examples section already follows the part); the rule's sections on the
   YAML key (Red Hat YAML hovers keys, and two hovers would stack); a
   heading per section (`###` is large in a hover; bold labels read as the
   examples section already does); collapsible sections (VS Code's hover
   verbosity is not in the protocol).

2. **The engine writes the hover for any editor; the client says what it
   can run and what it already shows.** A new initialization option,
   `hover`, has `commands` (the command ids the client runs from a hover
   link; none by default) and `problems` (`message`, the default, or
   `fixes`). The engine writes a `command:` link only to an id in
   `commands`, and otherwise names the action in words (*Fix: Add
   `maxItems: 1000` to `spec.ports`, a quick fix*). With `problems:
   fixes` the **Fix** block holds only the fix links, because the client
   already shows the problem's message in the same hover, as VS Code does;
   with `message` it holds the message too. VS Code sends `{"commands":
   ["cel-lens.applyFix", "cel-lens.pickKubernetesVersion"], "problems":
   "fixes"}`.

   Rejected: `command:` links for every client (Neovim and Helix show the
   raw link text); repeating the message in VS Code (it would show twice,
   one above the other).

3. **"What it checks" is a translation of the parsed expression, or
   nothing.** A new package, `internal/meaning`, turns cel-go's parsed
   expression into a sentence by a fixed table of shapes, and gives no
   sentence for anything outside it. Each boolean node must match a row;
   anything that is a value is quoted from the source in a code span, so
   the words never restate a value they could get wrong.

   | Shape | Sentence |
   | --- | --- |
   | `A && B && …` | *A, and B, and …* |
   | `A \|\| B \|\| …` | *A, or B, or …* |
   | `!has(x) \|\| B` | *when `x` is set, B* |
   | `c ? A : B` (both boolean) | *if C, then A; otherwise B* |
   | `x < y`, `<=`, `>`, `>=`, `==`, `!=` | *`x` is less than / at most / greater than / at least / equals / does not equal `y`* |
   | `size(x)` or `x.size()` compared | *the size of `x` is …* |
   | `has(x)`, `!has(x)` | *`x` is set*, *`x` is not set* |
   | `x in [a, b]`, `x in y` | *`x` is one of `a`, `b`*, *`x` is in `y`* |
   | `x.all(v, P)`, `x.exists(v, P)`, `x.exists_one(v, P)` (and `existsOne`, and the two-variable forms) | *for every / at least one / exactly one `v` in `x`, P* |
   | `x.startsWith(s)`, `endsWith`, `contains`, `matches(re)`, and each negated with `!` | *`x` starts with / ends with / contains `s`*, *matches the regular expression `re`*, *does not …* |
   | `isQuantity(x)`, `isURL(x)`, `isIP(x)`, `isCIDR(x)`, `isSemver(x)` | *`x` is a quantity / a URL / an IP address / a CIDR / a semantic version* |
   | a field read used as the result (`self.enabled`) | *`self.enabled` is true* |

   **No sentence is shown when** the expression is no check (it gets
   **Returns:**, below); the file's check has an error inside it; one
   boolean node matches no row (a `!` of a shape without a negative form, a
   call not in the table, a `true` or `false` literal, a comprehension
   macro other than those above); `&&` and `||` mix at one level without a
   comprehension or a ternary between them; or the sentence has more than
   four clauses or 200 characters as shown. Then the **Checks:** paragraph
   is left out; the other sections keep their order.

   A quoted value is the source text of that node, from the parser's
   offsets, on one line: line breaks and indentation outside string
   literals become one space, nothing inside a string changes. Variables
   keep their names (`self`, not "the widget"): the **Uses:** section says
   what each is.

   **Returns:** for an expression that is no check is a fixed sentence from
   its field, never wrong: a `messageExpression`, *the message the API
   server gives when the rule is false*; a policy variable, *the value of
   `variables.<name>`*; an audit annotation, *the value of the audit
   annotation `<key>`*; a mutation, *the apply configuration* or *the JSON
   patch the policy applies*. A match condition's sentence starts *the
   policy applies only when …*.

   Rejected: the rule's `message` as the sentence or its fallback (the
   author's words, which may be stale or wrong, as `message: must be below
   100` on `self < 1000`; the hover would vouch for them, and they are in
   the file one line below anyway); a partial sentence with `…` (dropping a
   `!` or a disjunct flips the meaning); a model (not deterministic, and the
   request asks for nothing invented); rewording values ("the port" for
   `p.port`), which needs a guess.

4. **The variables are those the expression reads, typed as the line
   types them, with why one is `dyn`, always.** **Uses:** lists, in the
   order first read, each variable of the scope the parsed expression
   reads (`self`, `oldSelf`, `object`, `oldObject`, `params`, `request`,
   `namespaceObject`, `authorizer`, each `variables.<name>`, and Kyverno's),
   not the comprehension's own variables, which the part hover types. Each
   has its type and a few words: `self`: `Widget.spec.ports[]` (object), the
   value the rule is on (the line's subject words, with the schema's type);
   `oldSelf`: the same, the value before the update, so the rule runs only
   on update; `object`: `apps/v1 Deployment`; `params`: `dyn`, *because
   spec.paramKind is no built-in kind and no CustomResourceDefinition among
   the workspace files defines it*. A type `.cellens` assumes says so, as
   the part hover does.

   **The reason a value is `dyn` becomes stable.** It is shown in **Uses:**
   and in the part hover whether `fitting-completion` is on or not, which
   keeps only the fitting completion. The reasons are already a fixed list
   with a sentence each and a test per fallback (`dyn.go`,
   `dynreport_test.go`), and the request asks for them. The README's table
   of experimental features and `docs/engine.md` say so.

   Rejected: listing every variable in scope (the request says "the
   variables it uses"); keeping the reason behind `fitting-completion` (the
   owner would not see it unless they turned on an experimental feature).

5. **The cost is the API server's own estimate, for the version picked.**
   A new `check.Costs` answers, for each CEL expression of a file, the
   numbers the API server would compute: for a CRD, the
   rule's or `messageExpression`'s estimated cost (`MaxCost` times the
   node's cardinality, as `getExpressionCost`), its budget
   (`StaticEstimatedCostLimit`), and its version's schema total
   (`TotalCost`, every rule and `messageExpression` of that schema) against
   `StaticEstimatedCRDCostLimit`, with the rules that add the most
   (`TotalCost.MostExpensive`). It uses the compiler `cost.go` already
   has, the version's `EnvSet`, under `UnderVersion`, so the numbers are
   those of a new CRD applied to the API server of that version. A test
   holds it to the API server: at every supported version, `Costs` puts an
   expression over its budget exactly when the validation reports its cost
   error, and a total over exactly when it reports the total's.

   Not estimated, with the reason in words: an expression that does not
   compile; a version's schema the API server compiles no rule of (the
   line's `not checked: schema error`); a ValidatingAdmissionPolicy or
   MutatingAdmissionPolicy (*the API server estimates no cost when a policy
   is applied; it stops an evaluation past 1,000,000*, linked to the
   *Runtime cost budget* section); a Kyverno ValidatingPolicy (*Kyverno
   estimates no cost when a policy is applied*). The update of an existing
   CRD whose rule did not change skips the per-expression limit (the API
   server's ratcheting); the hover judges as at creation, which is the
   stricter case and the one the request names.

   Rejected: an estimate for policies against their run-time limit (a
   number the API server never computes, and with `object` untyped the
   estimator assumes the largest sizes, so nearly every comprehension would
   look over); cel-go's own estimator without Kubernetes's (different
   numbers from the API server's).

6. **The wording, and "near" means half.** One paragraph, in the
   words of cel-lens's cost diagnostics (`count` for numbers: `12K`, `6.2M`,
   `2.4B`):
   - *estimated 1.2M of the per-expression budget of 10M (12%)*; under 1%
     reads *(under 1%)*, and a share is rounded down, so 99.6% reads 99%;
   - **near**, at half of a budget or more: *…(62%), half or more: doubling
     a limit it depends on can take it over*, then the causes `drivers`
     names (*Why: `spec.ports` is a list with no `maxItems`, and the
     estimator assumes up to 3.3M items*) and the fix (decision 7);
   - **over**: *estimated 2.4B, over the per-expression budget of 10M
     (240 times): the API server refuses this CustomResourceDefinition*;
     the fix is in the **Fix** block, since the cost problem covers the
     expression;
   - then the total: *All rules of `v1`'s schema: 6.4M of 100M (6%)*, near
     and over the same way, naming the three rules that add the most;
   - then the version, *Kubernetes 1.37*, a link to *Pick Kubernetes
     Version* when the client lists it.

   Half, because an estimate grows with each limit it depends on: a rule at
   half its budget goes over when one of those limits doubles, the most
   common edit to a limit. Rejected: 80% or 90% (a doubled `maxItems`
   already breaks the rule); 10% (most rules over lists would be flagged).
   A diagnostic for a near rule: not asked, and it would fill the Problems
   view with rules the API server takes.

7. **The fix is the quick fix that exists, reached from the hover.**
   - *Over budget*: the cost diagnostic's quick fix, as now, unchanged.
   - *Near*: the same `drivers` and `sizer`, with the budget as a
     parameter, sized to bring the expression under half its budget, so the
     hover stops saying near once applied. A fix exists only when a field
     the rule depends on has no limit; when every list, map and string it
     reads has one, the paragraph says so and offers no edit (lowering an
     existing limit is the author's call). The near fix is also offered as
     a quick fix at the expression, without a diagnostic, so the light bulb
     and the hover offer the same thing. When only the schema's total is
     near, the paragraph names the rules that add the most and offers no
     fix; each of those has its own when it is near its own budget.
   - *A link that applies it*: a new engine command, `cel-lens.applyFix`
     (`workspace/executeCommand`), takes `{uri, version, range, title}`,
     works out the code actions the engine would answer for that range of
     that document now, and applies the one with that title through
     `workspace/applyEdit` (labelled with the title), as the `.cellens`
     commands apply theirs. A document whose version moved since the hover
     is refused: *the file changed since this hover; hover again*. The
     hover's link is `command:cel-lens.applyFix?<the argument, as a
     URI-encoded JSON array>`, titled with the fix's title. The extension
     registers `cel-lens.applyFix` as its own command and forwards it with
     `runEngineCommand`, as it does every engine command.

   Rejected: putting the edit itself in the link (stale once the file
   changes, and a link that writes any edit it carries is what the trust
   rule is there to stop); asking VS Code for code actions by title
   (another extension's action could share the title, and the near fix has
   no diagnostic to look up).

8. **The docs links are pinned and checked before each release.**

   | For | Link |
   | --- | --- |
   | a CRD `rule` | `<k8s>/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/#validation-rules`; `#transition-rules` when it reads `oldSelf` |
   | a CRD `messageExpression` | the same page, `#the-messageexpression-field` |
   | a CRD rule's cost | the same page, `#resource-use-by-validation-functions` |
   | a ValidatingAdmissionPolicy | `<k8s>/docs/reference/access-authn-authz/validating-admission-policy/`, at `#validation-expression`, `#message-expression`, `#matching-requests-matchconditions`, `#variable-composition` or `#audit-annotations` |
   | a MutatingAdmissionPolicy | `<k8s>/docs/reference/access-authn-authz/mutating-admission-policy/`, at `#patch-type-apply-configuration` or `#patch-type-json-patch` for a mutation, the page for its match conditions and variables (it has no section of its own for them) |
   | a policy's run-time cost | `<k8s>/docs/reference/using-api/cel/#runtime-cost-budget` |
   | a Kyverno ValidatingPolicy | `https://release-1-19-0.kyverno.io/docs/policy-types/validating-policy/`, at `#using-messageexpression-to-generate-dynamic-messages` or `#using-auditannotations-to-add-custom-data` where they apply |
   | a standard CEL function or macro | `https://github.com/google/cel-spec/blob/v0.25.2/doc/langdef.md`, at its heading where it has one (`#regular-expressions`, `#list-operators`, `#string-functions`, `#presence-and-comprehension-macros`, …) |
   | a cel-go extension (strings, lists, math, sets, encoders, regex, bindings, two-variable comprehensions, optional values) | `https://pkg.go.dev/github.com/google/cel-go@v0.30.0/ext` at its `#hdr-<Name>-<Library>` heading; the optional values at `…/cel-go@v0.30.0/cel#hdr-<Name>-OptionalTypes` |
   | a Kubernetes CEL library function | `<k8s>/docs/reference/using-api/cel/` at its library's heading (`#kubernetes-list-library`, `-regex-`, `-url-`, `-ip-address-`, `-cidr-`, `-authorizer-`, `-format-`, `-quantity-`, `-semver-library`) |
   | a Kyverno library function | `https://release-1-19-0.kyverno.io/docs/policy-types/cel-libraries/` at its library's heading (`#resource-library`, `#http-library`, `#image-library`, `#globalcontext-library`, …) |

   `<k8s>` is the docs of the Kubernetes version picked:
   `https://kubernetes.io` for 1.37, the current release and the engine's
   default, and `https://v1-36.docs.kubernetes.io` (and `v1-35`, `v1-34`,
   `v1-33`) for the others; `v1-37.docs.kubernetes.io` does not exist until
   1.38 is released, and the engine release that adds 1.38 moves 1.37 to
   it. cel-spec is pinned to v0.25.2, the `cel.dev/expr` (cel-spec's Go
   module) the engine builds; cel-go to v0.30.0; Kyverno to its 1.19 docs,
   the version the engine checks policies with. Each was fetched on 14
   November and every heading named here answers (the plan's report lists
   the status codes).

   A table in `internal/assist` maps every function and macro the
   environments declare to its link; a test fails on one without, as
   `docs_internal_test.go` does for the examples. The check of every link
   needs the network, so it is a release step, a test run only with
   `CEL_LENS_LINKS_TEST=1` (as `CEL_LENS_DIST_TEST` is): it fetches each
   page, finds each heading's id (a GitHub page's from its raw Markdown's
   headings), and fails when the newest supported version's
   `v1-NN.docs.kubernetes.io` answers while the table still sends it to
   `kubernetes.io`.

   Rejected: unversioned links (a 1.33 cluster's rule linked to 1.37's
   page); one link for every function to the Kubernetes CEL page (today's
   fallback, which tells nothing about `lowerAscii`); a link check in `make
   check` (the network is not there in CI or the sandbox).

9. **Colour: the extension paints the hover's CEL in the colour style the
   editor uses.** The engine marks CEL as ` ```cel ` blocks, as it does
   now. The extension's hover middleware turns each into
   `<pre><code>` with one `<span style="color:#RRGGBB;">` per token, from
   the lexer `ColorPainter` uses and the palette of `cel-lens.colorStyle`
   for the document and the active theme's kind, each token's text
   HTML-escaped, and sets `supportHtml`. A `// …` comment after the code (the
   examples' `// -> true`) keeps the hover's own text colour. With the
   colour style `off` the block stays as the engine wrote it, as the
   editor's CEL is then uncoloured. Inline code (field paths, limits,
   quoted values) stays plain: in Markdown the engine cannot say which of
   it is CEL, and a field path coloured as CEL would be wrong.

   `supportHtml` lets HTML in the engine's Markdown through, so **every
   text the engine takes from a file is escaped** (descriptions, messages,
   names, kinds, paths), as `inline.escape` already escapes the examples';
   every code span and fenced block is delimited by a longer run of
   backticks than any it holds. This also closes a hole the trusted links
   would open: today a schema's `description` reaches the hover as
   written, so a description holding a `command:` link would become a live
   one.

   Rejected: a `cel` language and TextMate grammar (the theme's colours,
   not cel-lens's, so the hover and the editor would colour one rule two
   ways; and a second `cel` language would clash with other CEL
   extensions); bare `<span>`s without `supportHtml` (VS Code keeps them in
   trusted Markdown, but outside a code block, so in the proportional font);
   the engine writing HTML (other editors render none of it, and the colours
   are the extension's setting).

10. **Links run only cel-lens's own commands, and only the hover's.** The
    extension's middleware sets every hover's `isTrusted` to
    `{enabledCommands: HOVER_COMMANDS}`, never `true`, where
    `HOVER_COMMANDS` is `cel-lens.applyFix` and
    `cel-lens.pickKubernetesVersion`, the one constant it also sends as
    `hover.commands`. The engine writes no `command:` link to another id
    (decision 2). Tested where each lives: in the engine, every hover of the
    hover tests is scanned for `command:` links, each naming an id the test
    client listed, none with no list, and a description holding
    `[x](command:workbench.action.terminal.new)` or `<img src=…>` comes out
    escaped; in the extension, the middleware's output has exactly that
    `isTrusted`, `HOVER_COMMANDS` are contributed commands, hidden from the
    palette unless the palette already offers them, and equal to the
    `hover.commands` sent.

    Rejected: every `cel-lens.*` command (a crafted link in a workspace
    file could run *Restart Engine* or write a `.cellens` entry; the hover
    needs two); `isTrusted: true` (any command, which the style guide
    forbids).

11. **The hover stays quick: costs once per version, off the keystroke
    path.** The costs of a document are estimated on the first hover of a
    version of it that needs them, and kept until its text, the Kubernetes
    version or the features change; a near rule's fix is sized only when
    its hover is asked, and kept. The check of a document does not estimate
    costs (beyond what it does now for a cost error), so typing pays
    nothing. A cancelled hover (`$/cancelRequest`, which VS Code sends as
    the mouse moves on) stops the estimate and keeps nothing. Benches:
    - `go test -bench RuleHover ./internal/lsp/` on `BenchmarkHover`'s
      1,000-line CRD: the first hover after a change under 100 ms (a third
      of the diagnostics budget); a hover again within 1 ms of
      `BenchmarkHover` in the same run; the first hover on the code lens
      bench's CRD of 500 rules, reported;
    - a near rule's first hover, fix sized, under 300 ms;
    - `BenchmarkDiagnostics` and `BenchmarkHover` unchanged within noise.

    Rejected: estimating in every check (every pause in typing would pay
    for hovers nobody asks for); estimating in a background run after each
    check (the same cost, and a hover could still arrive first).

12. **Releases: the engine, then the extension that pins it.** The engine's
    steps ship as v0.34.0 with cel-lens-core#51 (issue #43), merged first;
    the extension pins it in `core-version.json` and ships as v0.43.0,
    after v0.42.0 (cel-lens#3). The README's *Hovers* section gets a picture
    the session takes (`npm run pictures`; the agents' sandbox has no
    Chromium libraries), and the session saves the hover in Default Dark
    Modern and Default Light Modern, on a part, off a name, on a near rule
    and on a problem, in theos `docs/screenshots/cel-lens-rule-hovers/`. The
    version numbers are the next minors unless another release comes first.

13. **Not in this plan.** A cost for policies or Kyverno (decision 5); the
    `message` as the sentence (decision 3); a near-limit diagnostic
    (decision 6); a fix for a total that is only near (decision 7);
    colouring inline code (decision 9); a hover on the YAML key (decision
    1); *Give it a type* in **Uses:** (the line above the rule has it); a
    cost on the command line (`cel-lens-core check`), which nobody asked
    for; changing the numbers the over-budget quick fix picks.

## Steps

Each is an issue in the session's Gitea (bodies in
[cloud-session/issues/](cloud-session/issues/)), stacked in this order.
Steps 1 to 7 are the engine's; step 8 needs v0.42.0 merged and an engine
candidate with steps 1 to 6.

| Step | Repository | What |
| --- | --- | --- |
| 1 | cel-lens-core | The rule hover's frame: anywhere in an expression, the fixed order and labels, the part on top, file text escaped and fences sized, the `hover` option, **Uses:** with types, and the `dyn` reason made stable (`h1.md`) |
| 2 | cel-lens-core | **Checks:** the package `internal/meaning`, a sentence for the table's shapes and none outside it; **Returns:** for expressions that are no check (`h2.md`) |
| 3 | cel-lens-core | `check.Costs`: the API server's estimate of every expression and each schema's total for the version picked, held to the API server's own cost errors; policies and Kyverno not estimated (`h3.md`) |
| 4 | cel-lens-core | **Cost:** in the hover: the wording, half as near, the version, kept per version and cancelled with the hover; the benches (`h4.md`) |
| 5 | cel-lens-core | Fixes from the hover: the near fix sized to half the budget and offered as a quick fix; the **Fix** block for problems at the cursor; `cel-lens.applyFix`; command links only to the client's list (`h5.md`) |
| 6 | cel-lens-core | **Docs:** the rule kind's page for the version picked, every function's pinned page, Kyverno's, and the release check of every link (`h6.md`) |
| 7 | cel-lens-core | Prepare v0.34.0 with #51; released with `release.yml` (`h7.md`) |
| 8 | cel-lens-vscode | The hover in VS Code: CEL blocks painted in the colour style, `isTrusted` limited to the hover's commands, `cel-lens.applyFix`, the `hover` option sent; e2e against the v0.34.0 candidate (`h8.md`) |
| 9 | cel-lens-vscode | Pin engine v0.34.0; the README's *Hovers* section and picture; Prepare v0.43.0, released with `release.yml`; #4 closed (`h9.md`) |

## Status

- **14 November, 07:00 UTC.** Planned. Nothing built.
