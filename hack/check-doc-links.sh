#!/usr/bin/env bash
# Checks the Markdown a reader of the public repository follows (#55, #56):
# no link goes to github.com/JairJosafath/c4factory, the private repository
# theos's history comes from (a number from there is plain text, c4factory
# #1094), and every relative link names a file or directory that exists.
# Prints each bad link as FILE:LINE: why, and exits 1.
# History is left alone: docs/design-history.md and docs/screenshots/ say what
# was true when they were written, and docs/plans/done/ may name c4factory as
# it was then. But a done plan's relative links are checked (#243): a plan moved
# there keeps links that must still resolve. A template's Markdown (templates/)
# links inside the repository it becomes, not this one. Inline code spans are
# not links.
# hack/check-doc-links_test.sh runs it on the repository in make check.
#
#   hack/check-doc-links.sh [ROOT]   the tracked *.md under ROOT (default: the repository)
set -euo pipefail

ROOT=${1:-$(cd "$(dirname "$0")/.." && pwd)}
cd "$ROOT"

fail=0
while IFS= read -r f; do
  case $f in
  docs/design-history.md | docs/screenshots/* | templates/* | */node_modules/*) continue ;;
  esac
  dir=$(dirname "$f")
  history=0
  [[ $f == docs/plans/done/* ]] && history=1
  n=0
  fence=0
  while IFS= read -r line || [ -n "$line" ]; do
    n=$((n + 1))
    # A fenced block is an example, not a link.
    if [[ $line == '```'* || $line == '~~~'* ]]; then
      fence=$((1 - fence))
      continue
    fi
    [ "$fence" = 0 ] || continue
    rest=$line
    # An inline code span is text, not a link (#243).
    while [[ $rest =~ \`\`[^\`]+\`\` ]]; do
      rest=${rest/"${BASH_REMATCH[0]}"/}
    done
    while [[ $rest =~ \`[^\`]*\` ]]; do
      rest=${rest/"${BASH_REMATCH[0]}"/}
    done
    while [[ $rest =~ \]\(([^\)\ ]+)[^\)]*\) ]]; do
      target=${BASH_REMATCH[1]}
      rest=${rest#*"${BASH_REMATCH[0]}"}
      case $target in
      *github.com/JairJosafath/c4factory*)
        [ "$history" = 0 ] || continue
        echo "$f:$n: links to the private c4factory repository; write c4factory #N as plain text"
        fail=1
        ;;
      *://* | mailto:* | '#'* | '<'*) ;;
      *)
        path=${target%%#*}
        path=${path%%\?*}
        [ -n "$path" ] || continue
        if [[ $path == /* ]]; then full=".$path"; else full="$dir/$path"; fi
        if [ ! -e "$full" ]; then
          echo "$f:$n: links to $target, which doesn't exist"
          fail=1
        fi
        ;;
      esac
    done
  done <"$f"
done < <(git ls-files -- '*.md' 2>/dev/null || find . -name '*.md' -type f | sed 's#^\./##')
exit $fail
