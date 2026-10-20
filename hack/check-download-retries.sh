#!/usr/bin/env bash
# Checks that every network fetch in the Dockerfiles' RUN steps retries, so one
# slow TLS handshake on a busy runner doesn't fail a whole release (#1494):
#   - curl has --retry N and --retry-all-errors (a cut-off transfer is not a
#     "transient" error to plain --retry), and writes to a file (-o): a retried
#     transfer piped to tar would send the bytes twice;
#   - go install, go get, go mod download, setup-envtest use (without -i) and
#     apk add run under retry, a shell function the same RUN defines;
#   - apt-get update and install set Acquire::Retries;
#   - npm ci and npm install set --fetch-retries;
#   - wget sets --tries.
# Comment lines are skipped and continued lines joined, as Docker does. Prints
# each fetch that doesn't retry and exits 1. hack/check-download-retries_test.sh
# runs it in make check on every Dockerfile Release builds.
#
#   hack/check-download-retries.sh DOCKERFILE...
set -euo pipefail

fail=0

# bad FILE CMD WHY: report one fetch.
bad() {
  echo "$1: $3: $2" >&2
  fail=1
}

# check_run FILE RUN: the checks above on one RUN instruction.
check_run() {
  local f=$1 run=$2 cmd seg words name defines=0
  local piped='(^|[[:space:]({])curl[[:space:]][^|]*[|]'
  [[ $run =~ (^|[^[:alnum:]_])retry\(\)[[:space:]]*\{ ]] && defines=1
  # Commands: split on && || and ;.
  while IFS= read -r cmd; do
    if [[ $cmd =~ $piped ]]; then
      bad "$f" "$cmd" "curl piped (a retry resends the bytes; download with -o)"
    fi
    # Simple commands: split further on pipes, subshells and braces.
    while IFS= read -r seg; do
      # The command's name: the first word after VAR=value and retry.
      read -ra words <<<"$seg" || true
      while [ ${#words[@]} -gt 0 ] && [[ ${words[0]} =~ ^[A-Za-z_][A-Za-z0-9_]*= || ${words[0]} == retry ]]; do
        words=("${words[@]:1}")
      done
      name=${words[0]:-}
      name=${name##*/}
      case $name in
        curl)
          [[ $seg =~ [[:space:]]--retry[[:space:]]+[0-9]+ ]] || bad "$f" "$seg" "curl without --retry N"
          [[ $seg == *--retry-all-errors* ]] || bad "$f" "$seg" "curl without --retry-all-errors"
          ;;
        wget) [[ $seg == *--tries* ]] || bad "$f" "$seg" "wget without --tries" ;;
        apt-get)
          if [[ " ${words[*]} " =~ [[:space:]](update|install)[[:space:]] ]] && [[ $seg != *Acquire::Retries* ]]; then
            bad "$f" "$seg" "apt-get without -o Acquire::Retries"
          fi
          ;;
        npm)
          if [[ " ${words[*]} " =~ [[:space:]](ci|install|i)[[:space:]] ]] && [[ $seg != *--fetch-retries* ]]; then
            bad "$f" "$seg" "npm without --fetch-retries"
          fi
          ;;
      esac
      if { [ "$name" = go ] && [[ " ${words[*]} " =~ ^\ go\ (install|get|mod\ download)\  ]]; } ||
        { [ "$name" = apk ] && [ "${words[1]:-}" = add ]; } ||
        { [ "$name" = setup-envtest ] && [ "${words[1]:-}" = use ] &&
          ! [[ " ${words[*]} " =~ [[:space:]](-i|--installed-only)[[:space:]] ]]; }; then
        if ! [[ $seg =~ ^[[:space:]]*retry[[:space:]] ]]; then
          bad "$f" "$seg" "not run under retry"
        elif [ "$defines" = 0 ]; then
          bad "$f" "$seg" "retry used but not defined in the same RUN"
        fi
      fi
    done < <(sed -E 's/\$\(|[|(){}]/\n/g' <<<"$cmd")
  done < <(sed -E 's/\$\{([^}]*)\}/$\1/g; s/&&|\|\||;/\n/g' <<<"$run")
}

for f in "$@"; do
  inst=
  while IFS= read -r line || [ -n "$line" ]; do
    # A comment line, even inside a continued instruction, is not part of it.
    [[ $line =~ ^[[:space:]]*# ]] && continue
    if [[ $line == *\\ ]]; then
      inst+="${line%\\} "
      continue
    fi
    inst+=$line
    read -r word rest <<<"$inst" || true
    if [ "${word^^}" = RUN ]; then
      # Drop the RUN flags (--mount=...) before the command.
      while [[ $rest == --* ]]; do read -r _ rest <<<"$rest" || true; done
      check_run "$f" "$rest"
    fi
    inst=
  done <"$f"
done
exit "$fail"
