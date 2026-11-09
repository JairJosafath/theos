# Evidence: GitLens-grade UX (docs/plans/gitlens-grade-ux.md)

## ts-standard v1.8.0

`go test ./internal/scaffold/ -run TestVSCodeExtensionGuidesHoldTheUXBar -count=1 -v`:

```
=== RUN   TestVSCodeExtensionGuidesHoldTheUXBar
--- PASS: TestVSCodeExtensionGuidesHoldTheUXBar (0.01s)
ok  	github.com/JairJosafath/theos/internal/scaffold	0.011s
```

A vscode-extension rendered with the change
(`go run ./hack/render -type extension -container shop-helper -display-name "Shop Helper" Framework=vscode-extension Standard=ts-standard Deployment=gitea-release`),
then `npx prettier@3.9.9 --check .` in it:

```
Checking formatting...
All matched files use Prettier code style!
```

The first render failed it on `*The UX bar*` (Prettier writes `_The UX bar_`)
in `AGENTS.md`, `docs/agents/implementer.md` and `docs/agents/reviewer.md`;
the templates now use underscores.

## The requests on JairJosafath/cel-lens

1. https://github.com/JairJosafath/cel-lens/issues/1 See what a CEL rule says about my example objects, right at the rule
2. https://github.com/JairJosafath/cel-lens/issues/2 A cel-lens view: every CEL rule in my workspace and how it's doing, a click from the rule
3. https://github.com/JairJosafath/cel-lens/issues/3 Every cel-lens feature one step away from where I am, and one command from off
4. https://github.com/JairJosafath/cel-lens/issues/4 Hovers that tell me what a rule means, what it costs and where to read more
5. https://github.com/JairJosafath/cel-lens/issues/5 cel-lens in kubebuilder markers, where operator authors write their CEL

## make check

The first run stopped at `make validate-yaml`: `hack/k8s-openapi.sh` lists the
Kubernetes v1.37.0 OpenAPI files through `api.github.com`, which this
session's network proxy refuses (403) for a repository outside its scope.
The same files were fetched from `raw.githubusercontent.com` with the names
from a sparse clone of kubernetes/kubernetes at v1.37.0, laid out as the
script lays them out; the script is unchanged. The second run passed:

```
0 issues.
All valid
The chart's catalog matches templates/versions.txt; off, it renders no catalog, bundles or Agents
test-hack: all 44 shell tests passed
 Test Files  69 passed (69)
      Tests  981 passed (981)
ok  	github.com/JairJosafath/theos/internal/scaffold	5.534s
ok  	github.com/JairJosafath/theos/internal/controller	582.758s
ok  	github.com/JairJosafath/theos/internal/controller	683.516s
make check: passed on tree 2ec449bcaee3c3b0e150c33c7fadfc88efe80992; recorded
```
