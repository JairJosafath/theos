#!/usr/bin/env bash
# Drafts a release's notes in Keep a Changelog categories from the pull requests
# merged since the previous tag, then the image list and the install command.
# The Release workflow writes its GitHub release from it; the maintainer edits
# the draft there, so it only has to be close.
#
#   hack/release-notes.sh [TO [FROM]]       the pull requests merged in FROM..TO
#   hack/release-notes.sh --prs FILE [FROM] the pull requests in FILE instead
#
# TO defaults to HEAD, FROM to the newest vX.Y.Z tag before TO (the whole
# history when there is none). A pull request is a first-parent merge commit
# "Merge pull request #N from owner/branch": its title is the commit's body, its
# files what it changed, its body the messages of the commits it merged, and its
# labels come from gh (none when gh can't say).
#
# FILE holds one JSON object per line:
#   {"number":N,"title":"...","branch":"...","labels":[...],"files":[...],"body":"..."}
#
# Where a pull request goes:
#   - left out when every file it touches is under hack/, .claude/, .relay/ or docs/dev/;
#   - Security when a label or its title says security;
#   - Changed, marked **Breaking**, when its type ends in ! or its body says BREAKING CHANGE;
#   - left out when its type is chore, ci, test or docs;
#   - Added for feat, Fixed for fix, Changed for any other type.
# Its type is the title's conventional-commit prefix (feat(12)!: ...), else the
# branch's first segment (feat/12-...). Each line is the title without that
# prefix, then (#N).
#
#   VERSION   the version the image list and install command name (default TO without its v)
#   REGISTRY  the images' registry (default ghcr.io/jairjosafath/c4factory)
#   REPO      owner/name, for the getting-started link (default JairJosafath/c4factory)
set -euo pipefail

prs=
if [ "${1:-}" = --prs ]; then
  prs=${2:?--prs needs a FILE}
  from=${3:-} to=HEAD
else
  to=${1:-HEAD}
  from=${2:-$(git describe --tags --abbrev=0 --match 'v[0-9]*.[0-9]*.[0-9]*' "$to^" 2>/dev/null || true)}
fi
REPO=${REPO:-JairJosafath/c4factory}
REGISTRY=${REGISTRY:-ghcr.io/jairjosafath/c4factory}
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
    ([.title | capture("^(?<type>[a-zA-Z]+)(\\([^)]*\\))?(?<bang>!?): *(?<rest>.*)$")] | first // {}) as $cc
    | ($cc.type // (.branch | split("/")[0]) | ascii_downcase) as $type
    | ($cc.rest // .title) as $text
    | select(any(.files[]; test("^(hack|\\.claude|\\.relay|docs/dev)/") | not) or (.files | length == 0))
    | if (any(.labels[]; test("security"; "i")) or (.title | test("security"; "i"))) then
        "Security\t- \($text) (#\(.number))"
      elif ($cc.bang == "!" or (.body | test("BREAKING[ -]CHANGE"))) then
        "Changed\t- **Breaking:** \($text) (#\(.number))"
      elif ($type | IN("chore", "ci", "test", "docs")) then empty
      elif $type == "feat" then "Added\t- \($text) (#\(.number))"
      elif $type == "fix" then "Fixed\t- \($text) (#\(.number))"
      else "Changed\t- \($text) (#\(.number))"
      end'
}

if [ -n "$prs" ]; then sorted=$(sort_prs <"$prs"); else sorted=$(collect | sort_prs); fi

listed=0
for section in Added Changed Fixed Security; do
  lines=$(printf '%s\n' "$sorted" | sed -n "s/^$section\t//p")
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
base and go, agent-base, agent-go, gitea-runner-base and gitea-runner-go.

Install: [Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox) first,
then a namespace for each project, then the chart with the projects in \`gitea.projects\`:

\`\`\`bash
kubectl apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.4/sandbox.yaml
kubectl create namespace webshop
helm install c4-factory oci://$REGISTRY/charts/c4-factory --version $VERSION \\
  -n c4-factory-system --create-namespace --wait --timeout 10m \\
  --set 'gitea.projects={webshop}'
\`\`\`

[docs/getting-started.md](https://github.com/$REPO/blob/$TAG/docs/getting-started.md)
goes on to a first app: the agents' Claude token, a model, an Agent and a Crew,
and pulling private images.
EOF
