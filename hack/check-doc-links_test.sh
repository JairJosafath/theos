#!/usr/bin/env bash
# Tests for hack/check-doc-links.sh (#55, #56): the repository's own Markdown
# passes; a link to the private c4factory repository or to a file that isn't
# there fails with the file and line; history, templates, fenced examples,
# web links and anchors are left alone.
# Run: hack/check-doc-links_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT=$(cd "$(dirname "$0")/.." && pwd)
CHECK=$ROOT/hack/check-doc-links.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

out=$("$CHECK" "$ROOT" 2>&1)
check "the repository's Markdown has no private or dead link (#55, #56)" "$?" 0
[ -z "$out" ] || echo "$out"

# tree: an empty repository under $T/r with the files the test writes.
tree() {
  rm -rf "$T/r"
  mkdir -p "$T/r/docs/plans/done" "$T/r/docs/screenshots" "$T/r/templates/x"
  git -C "$T/r" init -q
}
run() {
  git -C "$T/r" add -A
  out=$("$CHECK" "$T/r" 2>&1)
  rc=$?
}

tree
printf '%s\n' '# A' 'See [the guide](docs/guide.md#install) and [the web](https://example.com/x.md).' \
  'An [anchor](#here), a [mail](mailto:a@example.com), a [dir](docs/).' >"$T/r/README.md"
echo '[up](../README.md)' >"$T/r/docs/guide.md"
run
check "links to files, directories, anchors and the web pass" "$rc" 0

tree
printf '%s\n' 'one' 'See [#1094](https://github.com/JairJosafath/c4factory/issues/1094).' >"$T/r/README.md"
run
check "a link to c4factory fails (#56)" "$rc" 1
check "it names the file and line" "$(grep -c '^README.md:2: links to the private c4factory' <<<"$out")" 1

tree
echo 'See [the plan](docs/gone.md).' >"$T/r/README.md"
run
check "a link to a file that isn't there fails (#55)" "$rc" 1
check "it names the target" "$(grep -c "^README.md:1: links to docs/gone.md, which doesn't exist" <<<"$out")" 1

tree
echo 'ok' >"$T/r/README.md"
echo '[old](https://github.com/JairJosafath/c4factory/issues/1) [ok](../../../README.md)' >"$T/r/docs/plans/done/p.md"
echo '[old](https://github.com/JairJosafath/c4factory/issues/1)' >"$T/r/docs/design-history.md"
echo '[gone](gone.md)' >"$T/r/docs/screenshots/s.md"
echo '[its own](docs/style/go.md)' >"$T/r/templates/x/README.md"
run
check "history and templates are left alone" "$rc" 0

tree
printf '%s\n' 'text' '```' '[example](gone.md)' '```' >"$T/r/README.md"
run
check "a link in a fenced block is an example" "$rc" 0

tree
echo 'Two: [a](README.md) and [b](gone.md "title").' >"$T/r/README.md"
run
check "the second link on a line is checked, a title ignored" "$(grep -c 'links to gone.md' <<<"$out")" 1

tree
echo 'ok' >"$T/r/README.md"
echo 'See [gone](gone.md).' >"$T/r/docs/plans/done/p.md"
run
check "a broken relative link in a done plan fails (#243)" "$rc" 1
check "it names the done plan and line (#243)" "$(grep -c "^docs/plans/done/p.md:1: links to gone.md, which doesn't exist" <<<"$out")" 1

tree
printf '%s\n' 'Code: `[x](gone.md)` and `a` then [ok](README.md).' >"$T/r/README.md"
cp "$T/r/README.md" "$T/r/docs/plans/done/p.md"
sed -i 's#(README.md)#(../../../README.md)#' "$T/r/docs/plans/done/p.md"
run
check "link-like text in an inline code span is not a link (#243)" "$rc" 0

tree
printf '%s\n' 'Code: `[x](gone.md)` and [y](gone.md).' >"$T/r/README.md"
run
check "a real link after a code span is still checked (#243)" "$(grep -c 'links to gone.md' <<<"$out")" 1

exit $fail
