#!/usr/bin/env bash
# Downloads the built-in OpenAPI v3 schemas of one Kubernetes release into DIR,
# laid out the way kubectl-validate --local-schemas reads them (api/v1.json,
# apis/<group>/<version>.json), so make validate-yaml checks the built-in kinds
# offline. kubectl-validate embeds schemas only up to an older release.
#
#   hack/k8s-openapi.sh v1.37.0 bin/k8s-openapi-v1.37.0
set -euo pipefail

tag=${1:?usage: k8s-openapi.sh TAG DIR}
dir=${2:?usage: k8s-openapi.sh TAG DIR}
[ -f "$dir/api/v1.json" ] && exit 0

# Worktrees share the tool directory, so each download goes to its own folder
# and the first to finish is kept.
mkdir -p "$(dirname "$dir")"
tmp=$(mktemp -d "$dir.tmp.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
mkdir -p "$tmp/api" "$tmp/apis"
echo "Downloading the Kubernetes $tag OpenAPI schemas into $dir"
# The listing goes through the GitHub API, which limits anonymous calls; CI passes a token.
auth=()
[ -z "${GITHUB_TOKEN:-}" ] || auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
names=$(curl -fsSL ${auth[@]+"${auth[@]}"} "https://api.github.com/repos/kubernetes/kubernetes/contents/api/openapi-spec/v3?ref=$tag" |
	sed -n 's/.*"name": *"\([^"]*_openapi\.json\)".*/\1/p')
for name in $names; do
	# api__v1_openapi.json -> api/v1.json; apis__apps__v1_openapi.json -> apis/apps/v1.json.
	# Group listings (apis__apps_openapi.json) have no version and are skipped.
	path=${name%_openapi.json}
	case "$path" in
	api__*) out="api/${path#api__}.json" ;;
	apis__*__*) rest=${path#apis__}; out="apis/${rest%%__*}/${rest#*__}.json" ;;
	*) continue ;;
	esac
	mkdir -p "$tmp/$(dirname "$out")"
	curl -fsSL -o "$tmp/$out" "https://raw.githubusercontent.com/kubernetes/kubernetes/$tag/api/openapi-spec/v3/$name"
done
[ -f "$tmp/api/v1.json" ] || { echo "no api/v1 schema for $tag" >&2; exit 1; }
[ -f "$dir/api/v1.json" ] || { rm -rf "$dir"; mv "$tmp" "$dir"; }
