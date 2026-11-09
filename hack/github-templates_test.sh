#!/usr/bin/env bash
# Tests for the files a person from outside meets on GitHub (#48): each issue
# form is a form GitHub accepts (name, description, a body whose inputs have
# distinct ids) and labels issues only with labels hack/lib/github.sh makes;
# blank issues are off and the chooser sends a vulnerability to SECURITY.md;
# the pull request template asks for the issue, the evidence and what wasn't
# tested; and CODEOWNERS names every one of the factory-self Standard's
# protectedPaths (dist/chart/values.yaml), so a change to the fence asks the
# owner.
# Run: hack/github-templates_test.sh
ROOT=$(cd "$(dirname "$0")/.." && pwd)
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
FORMS=$ROOT/.github/ISSUE_TEMPLATE

for form in bug feature template user-report; do
  f=$FORMS/$form.yml
  check "$form.yml exists" "$([ -f "$f" ] && echo yes)" yes
  [ -f "$f" ] || continue
  check "$form.yml has a name" "$(grep -c '^name: .' "$f")" 1
  check "$form.yml has a description" "$(grep -c '^description: .' "$f")" 1
  check "$form.yml has a body" "$(grep -c '^body:$' "$f")" 1
  ids=$(sed -n 's/^    id: //p' "$f")
  check "$form.yml's ids are distinct" "$(sort <<<"$ids" | uniq -d | wc -l)" 0
  check "$form.yml requires at least one answer" "$([ "$(grep -c 'required: true' "$f")" -ge 1 ] && echo yes)" yes
  # Every label a form sets is one the repository has: gh_labels' list.
  for label in $(sed -n 's/^labels: *\[\(.*\)\]$/\1/p' "$f" | tr ',' ' '); do
    check "$form.yml's label $label is one hack/lib/github.sh makes" \
      "$(grep -c "^  '$label|" "$ROOT/hack/lib/github.sh")" 1
  done
done
# hack/lib/github.sh intake reads the reporter under this heading (gh_reporter).
check "user-report.yml keeps the Reported by field intake reads" \
  "$(grep -c 'label: Reported by' "$FORMS/user-report.yml")" 1
for form in bug feature template; do
  check "$form.yml has no Reported by field, so intake labels it source/dev" \
    "$(grep -c 'label: Reported by' "$FORMS/$form.yml")" 0
done

check "blank issues are off" "$(grep -c '^blank_issues_enabled: false$' "$FORMS/config.yml")" 1
check "the chooser sends a vulnerability to SECURITY.md" "$(grep -c 'SECURITY.md$' "$FORMS/config.yml")" 1

PR=$ROOT/.github/pull_request_template.md
for want in '^Closes #' '^## What changed' '^## Evidence' '^## Not tested'; do
  check "the pull request template has $want" "$(grep -c "$want" "$PR")" 1
done

OWNERS=$ROOT/.github/CODEOWNERS
check "CODEOWNERS has a default owner" "$(grep -c '^\* \+@' "$OWNERS")" 1
paths=$(grep -A12 'name: factory-self' "$ROOT/dist/chart/values.yaml" | sed -n 's/^ *protectedPaths: *\[\(.*\)\]$/\1/p' | tr -d ' ' | tr ',' '\n')
check "values.yaml lists the factory-self Standard's protectedPaths" "$([ "$(wc -l <<<"$paths")" -ge 5 ] && echo yes)" yes
for p in $paths; do
  check "CODEOWNERS names the protected path $p" "$(grep -c "^/$p \+@" "$OWNERS")" 1
done

exit $fail
