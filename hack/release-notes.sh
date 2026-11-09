#!/usr/bin/env bash
# Drafts a release's notes in Keep a Changelog categories from the pull requests
# merged since the previous release, then the image list and the install
# command. The Release workflow writes its GitHub release from it; the
# maintainer edits the draft there, so it only has to be close.
#
#   hack/release-notes.sh [TO [FROM]]       the pull requests merged in FROM..TO
#   hack/release-notes.sh --prs FILE [FROM] the pull requests in FILE instead
#
# TO defaults to HEAD. FROM given empty is the whole history; left out, it is
# the newest vX.Y.Z tag before TO (the whole history when there is none). The
# Release workflow passes hack/previous-release.sh's answer, the newest
# published release before TO, so a tag whose release failed doesn't cut the
# notes short: they list everything since the last release devices got.
#
# A pull request is a first-parent merge commit "Merge pull request #N from
# owner/branch": its title is the commit's body, its files what it changed, its
# body the messages of the commits it merged, and its labels come from gh (none
# when gh can't say).
#
# FILE holds one JSON object per line:
#   {"number":N,"title":"...","branch":"...","labels":[...],"files":[...],"body":"..."}
#
# Where a pull request goes:
#   - left out when every file it touches is the developers' own: under hack/,
#     .claude/, .relay/, docs/dev/, docs/plans/ or docs/screenshots/, or one of
#     docs/design.md, docs/architecture.md, AGENTS.md, CLAUDE.md, DEVELOPMENT.md,
#     GOVERNANCE.md and ROADMAP.md (a plan's pull request, #1270);
#   - Security when a label or its title says security;
#   - Changed, marked **Breaking**, when its type ends in ! or its body says BREAKING CHANGE;
#   - left out when its type is chore, ci, test or docs;
#   - Added for feat, Fixed for fix, Changed for any other type;
#   - and, whatever its type, a line under Deprecated for each line of its
#     merged commits' bodies saying "Deprecated: <text>", under Removed for
#     each saying "Removed: <text>" (an API version, #1618).
# Its type is the title's conventional-commit prefix (feat(12)!: ...), else the
# branch's first segment (feat/12-...). A prefix counts only when it names a
# Conventional Commits type; "AWS: ..." or "Plan: ..." is part of the title
# (#1270). Each line is the title without a counted prefix, then (#N).
#
#   VERSION   the version the image list and install command name (default TO without its v)
#   REGISTRY  the images' registry (default ghcr.io/jairjosafath/theos)
#   REPO      owner/name, for the getting-started link (default JairJosafath/theos)
set -euo pipefail

prs=
if [ "${1:-}" = --prs ]; then
  prs=${2:?--prs needs a FILE}
  from=${3:-} to=HEAD
else
  to=${1:-HEAD}
  from=${2-$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' "$to^" 2>/dev/null || true)}
fi
REPO=${REPO:-JairJosafath/theos}
REGISTRY=${REGISTRY:-ghcr.io/jairjosafath/theos}
if [ -z "${VERSION:-}" ]; then
  case $to in v[0-9]*) VERSION=${to#v} ;; *) VERSION=X.Y.Z ;; esac
fi
TAG=v$VERSION

# collect: one JSON line per pull request merged in from..to, oldest first.
collect() {
  local range=$to sha subject n branch title labels
  [ -n "$from" ] && range=$from..$to
  git log --first-parent --merges --reverse --format='%H %s' "$range" |
    while read -r sha subject; do
      [[ $subject =~ ^Merge\ pull\ request\ \#([0-9]+)\ from\ [^/]+/(.+)$ ]] || continue
      n=${BASH_REMATCH[1]} branch=${BASH_REMATCH[2]}
      title=$(git log -1 --format=%b "$sha" | sed '/^[[:space:]]*$/d' | head -n 1)
      labels=$(gh pr view "$n" --repo "$REPO" --json labels -q '[.labels[].name]' 2>/dev/null || echo '[]')
      jq -cn --argjson number "$n" --arg title "${title:-$branch}" --arg branch "$branch" \
        --argjson labels "${labels:-[]}" \
        --arg files "$(git diff --name-only "$sha^1" "$sha")" \
        --arg body "$(git log --format=%B "$sha^1..$sha^2")" \
        '{number: $number, title: $title, branch: $branch, labels: $labels,
          files: ($files | split("\n") | map(select(. != ""))), body: $body}'
    done
}

# sort: each pull request as "SECTION<TAB>line", or nothing when it's left out.
sort_prs() {
  jq -r '
    ([.title | capture("^(?<type>[a-zA-Z]+)(\\([^)]*\\))?(?<bang>!?): *(?<rest>.*)$")
      | select(.type | ascii_downcase
          | IN("feat", "fix", "docs", "chore", "ci", "test", "refactor", "perf", "build", "style", "revert"))]
      | first // {}) as $cc
    | ($cc.type // (.branch | split("/")[0]) | ascii_downcase) as $type
    | ($cc.rest // .title) as $text
    | select(any(.files[]; test("^(hack|\\.claude|\\.relay|docs/dev|docs/plans|docs/screenshots)/|^docs/(design|architecture)\\.md$|^(AGENTS|CLAUDE|DEVELOPMENT|GOVERNANCE|ROADMAP)\\.md$") | not)
        or (.files | length == 0))
    | if (any(.labels[]; test("security"; "i")) or (.title | test("security"; "i"))) then
        "Security\t- \($text) (#\(.number))"
      elif ($cc.bang == "!" or (.body | test("BREAKING[ -]CHANGE"))) then
        "Changed\t- **Breaking:** \($text) (#\(.number))"
      elif ($type | IN("chore", "ci", "test", "docs")) then empty
      elif $type == "feat" then "Added\t- \($text) (#\(.number))"
      elif $type == "fix" then "Fixed\t- \($text) (#\(.number))"
      else "Changed\t- \($text) (#\(.number))"
      end,
      (.number as $n | .body | split("\n")[]
        | capture("^(?<section>Deprecated|Removed): *(?<text>.+?)\\s*$")
        | "\(.section)\t- \(.text) (#\($n))")'
}

if [ -n "$prs" ]; then sorted=$(sort_prs <"$prs"); else sorted=$(collect | sort_prs); fi

listed=0
for section in Added Changed Deprecated Removed Fixed Security; do
  lines=$(printf '%s\n' "$sorted" | sed -n "s/^$section\t//p" | awk '!seen[$0]++')
  [ -n "$lines" ] || continue
  [ $listed = 0 ] || echo
  printf '### %s\n\n%s\n' "$section" "$lines"
  listed=1
done
if [ $listed = 0 ]; then
  echo "Nothing a device runs changed${from:+ since $from}."
fi

cat <<EOF

Images: \`$REGISTRY/<name>:$VERSION\` for operator, ui, seed, the toolchains
base and go, agent-base, agent-go, gitea-runner-base, gitea-runner-go,
gitea-runner-image, and the test environments env-browser, env-android and
env-godot.

Install, as the guide below does without a pull secret: [Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox)
first, its controller without leader election (the guide says why), then the
release's namespace and Gitea's, then the chart with the projects in
\`gitea.projects\`, for each of which the chart makes a namespace:

\`\`\`bash
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.4/sandbox.yaml
kubectl -n agent-sandbox-system patch deployment agent-sandbox-controller --type=json \\
  -p '[{"op":"replace","path":"/spec/template/spec/containers/0/args/0","value":"--leader-elect=false"}]'
for ns in theos-system gitea; do
  kubectl create namespace \$ns --dry-run=client -o yaml | kubectl apply -f -
done
helm install theos oci://$REGISTRY/charts/theos --version $VERSION \\
  -n theos-system --wait --timeout 10m \\
  --set gitea.createNamespace=false \\
  --set 'gitea.projects={webshop}'
\`\`\`

[docs/getting-started.md](https://github.com/$REPO/blob/$TAG/docs/getting-started.md)
goes on to a first app: the agents' Claude token, a model, an Agent and a Crew,
and pulling private images.
EOF
