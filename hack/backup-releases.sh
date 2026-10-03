#!/usr/bin/env bash
# Back up the source zip of every release tag of a Gitea org into backups/.
# Usage: PROJECT=cel-lens PUSH=1 hack/backup-releases.sh   (see backups/README.md)
set -euo pipefail

PROJECT="${PROJECT:-cel-lens}"
PUSH="${PUSH:-1}"
cd "$(git rev-parse --show-toplevel)"

PF_PID=""
cleanup() { [ -z "$PF_PID" ] || kill "$PF_PID" 2>/dev/null || true; }
trap cleanup EXIT

ADMIN_PW=$(kubectl -n gitea get secret gitea-admin -o jsonpath='{.data.password}' | base64 -d)
api() { curl -fsS -u "c4admin:$ADMIN_PW" "$@"; }

BASE=http://localhost:3000
if ! curl -s -o /dev/null --max-time 3 "$BASE/api/healthz"; then
  # No `make gitea-forward` running: start our own on a free port.
  port=3931
  while (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; do port=$((port + 1)); done
  kubectl -n gitea port-forward svc/gitea "$port:3000" >/dev/null 2>&1 &
  PF_PID=$!
  BASE="http://localhost:$port"
  curl -s -o /dev/null --max-time 5 --retry 30 --retry-delay 1 --retry-connrefused "$BASE/api/healthz" ||
    { echo "cannot reach Gitea through kubectl port-forward" >&2; exit 1; }
fi

added=()
repos=$(api "$BASE/api/v1/orgs/$PROJECT/repos?limit=50" | jq -r '.[].name')
for repo in $repos; do
  dir="backups/$PROJECT/$repo"
  tags=$(api "$BASE/api/v1/repos/$PROJECT/$repo/tags?limit=50" | jq -r '.[].name')
  for tag in $tags; do
    zip="$dir/$repo-$tag.zip"
    [ -e "$zip" ] && continue
    mkdir -p "$dir"
    tmp="$zip.part"
    api -o "$tmp" "$BASE/api/v1/repos/$PROJECT/$repo/archive/$tag.zip"
    if ! unzip -tq "$tmp" >/dev/null; then
      rm -f "$tmp"
      echo "zip of $repo $tag is corrupt" >&2
      exit 1
    fi
    mv "$tmp" "$zip"
    (cd "$dir" && sha256sum "$(basename "$zip")" >>SHA256SUMS)
    echo "added $zip ($(du -h "$zip" | cut -f1))"
    added+=("$repo $tag")
  done
done

[ ${#added[@]} -gt 0 ] || exit 0

list=$(printf '%s, ' "${added[@]}")
msg="chore(backups): back up $PROJECT release sources (${list%, })"
git add -- backups/
git commit -q -m "$msg" -- backups/
echo "$msg"
if [ "$PUSH" = 1 ]; then
  git push
fi
