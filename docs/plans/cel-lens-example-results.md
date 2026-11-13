# Plan: what a CEL rule says about example objects, at the rule

Request: JairJosafath/cel-lens#1, the owner's, of 9 November 2026: "The
reason I'd install cel-lens is still under *Not supported yet*: dry runs of
CEL against sample objects." Written on 13 November by the session the owner
ran ("Continue work via theos on cel-lens"). It is the next request after
cel-lens#6, which [cel-lens-releases-itself.md](cel-lens-releases-itself.md)
releases.

## Goal

While a person edits a rule in a CRD's `x-kubernetes-validations` or in an
admission policy, the line above the rule says what the rule does to the
example objects of the workspace, without a cluster: how many it lets
through, how many it refuses, and whether that is what each example
expects. A wrong verdict stands out; a right one stays quiet.

Done when the request's own list holds on a released extension, and the
README shows it with a picture and no longer lists dry runs under *Not
supported yet*.

## Where things stand

- The engine compiles every rule with the API server's own libraries
  (`k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel`,
  `k8s.io/apiserver/pkg/admission/plugin/cel`) for the Kubernetes version
  picked, and evaluates nothing.
- It already finds a kind's examples: `cel-lens.typeChoices` lists the
  documents of the workspace of a value's kind (at most 100), and
  `cel-lens.inferType` reads them. *Give it a type* shows them under
  *Examples in the workspace*.
- The line above each rule (`internal/inline`) has parts, each a command;
  the extension draws it with no code of its own.

## Decisions

1. **An example is any document of the workspace of the rule's kind.** For
   a CRD's rule: a YAML document whose `apiVersion` and `kind` are a version
   the CRD serves. Nothing to register and no folder to name: a kubebuilder
   project's `config/samples/` is found as it is, and so is a `testdata/`
   folder beside the CRD. It is the list *Give it a type* already shows, so
   a person learns one idea of "example". Rejected: a manifest that lists
   examples per rule (Kyverno's `kyverno-test.yaml`, Chainsaw): setup before
   the first result, which the request rules out.

2. **An example expects to be let through unless it says it is refused.**
   It says so in one of two ways, the guess and the exact one:
   - its path: a folder or a word of the file name is `invalid`, `bad`,
     `refused`, `rejected` or `denied` (`testdata/invalid/big.yaml`,
     `widget-invalid.yaml`); theos keeps its own failing cases in
     `test/invalid/`;
   - a comment at the top of the document, which wins over the path:
     `# cel-lens: refused`, optionally with a part of the message it expects
     (`# cel-lens: refused "at most 10"`), or `# cel-lens: allowed`.

   An example that should be refused is right when at least one rule, or
   the schema, refuses it, and with a message part, when that message does.
   So per rule: refusing a refused example is quiet, refusing an allowed
   one is wrong and shows at that rule. An example that should be refused
   and that nothing refuses is wrong and shows on the example, where the
   person reads why.

3. **An update is the same object twice in one file.** Two documents of one
   kind, namespace and name in one file, in order: the first is created, the
   second is an update from the first, so rules with `oldSelf` (and a
   policy's `oldObject`) get both. `kubectl apply -f` of that file does
   exactly that. A third is an update from the second. No directive, and a
   transition rule with no such pair says so in its line (`no update
   example`) instead of reading as passing.

4. **The API server's code gives the verdict.** For a CRD: its defaulting,
   its schema validation, then its CEL validator with its cost budget, each
   for the Kubernetes version picked. The message shown is the one the API
   server returns (`spec.replicas: Invalid value: ...`). Nothing is
   reimplemented, so there is no second opinion to drift. An example the
   schema refuses says so on the example, at the field, as a rule's compile
   error shows on the rule; its CEL rules still run where the API server
   would run them.

5. **CRDs first, then admission policies.** A ValidatingAdmissionPolicy's
   examples are the documents its `matchConstraints` match, with `params`
   from the workspace document its binding names; `request` is a create or,
   for a pair, an update, by no user in particular. Its own step, after the
   CRD steps have shown the shape. Kyverno policies wait for a request.

6. **One quiet part of the line, with the wrong first.**
   `self: Widget.spec · compiles · 3 examples pass`; wrong:
   `refuses 1 example it should allow · 2 pass`. The part's command lists
   each example with its verdict and the API server's message and goes to
   the example. The rule's hover has the same list. A wrong verdict is also
   a warning on the rule and on the example, so it is in the Problems view
   and at both ends. The wording lives in one table-tested place: it is the
   UI.

7. **Results never hold up typing.** They are computed off the request
   path, per rule and example, kept until the rule, the schema, the example
   or the version changes, and bounded: at most the 100 examples the engine
   lists for a kind, each under the API server's cost budget, a changed rule
   or example evaluated after typing pauses. The line shows what is known
   and fills in.

8. **One setting, one command.** `cel-lens.exampleResults` (on by default)
   and **cel-lens: Toggle Example Results**; off means nothing is evaluated.
   Stable, not experimental: it is the reason to install, and the README
   says what it covers.

9. **The command line gives the same results.** `cel-lens-core examples`
   fails on a wrong verdict, which makes the examples a test in CI. Its own
   step, after the editor's.

## Steps

Each is an issue in the session's Gitea (bodies in
[cloud-session/issues/](cloud-session/issues/)), stacked in this order.

| Step | Repository | What |
| --- | --- | --- |
| 1 | cel-lens-core | A CRD's examples and what each expects: found, read, updates paired, and checked against the schema, with the schema's problems on the example (`x1.md`) |
| 2 | cel-lens-core | The CRD's rules run on each example with the API server's validator, creates and updates; `cel-lens.exampleResults` answers per rule and example (`x2.md`) |
| 3 | cel-lens-core | The line's part, the hover's list, the warnings, the setting |
| 4 | cel-lens-core | Admission policies: examples by `matchConstraints`, `params` by binding |
| 5 | cel-lens-core | `cel-lens-core examples`: the same results from the command line, failing on a wrong verdict |
| 6 | cel-lens-vscode | The list of examples behind the part, the toggle, the status bar entry |
| 7 | cel-lens-vscode | Pin the engine release with it; the README's section and picture; *Not supported yet* loses the line |

## Status

- **13 November.** Planned. Nothing built.
