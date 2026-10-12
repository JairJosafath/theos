#!/usr/bin/env bash
# Checks that every base image of the Dockerfiles is pinned by digest
# (name:tag@sha256:...), so a tag that moves can't change what a release
# builds (#912). A FROM naming an earlier stage passes; a FROM ${VAR} is checked
# through VAR's ARG default. Prints each FROM that isn't pinned and exits 1.
# hack/check-pinned-images_test.sh runs it in make check on the Dockerfiles
# already pinned; add one there when its images are (#919).
#
#   hack/check-pinned-images.sh DOCKERFILE...
set -euo pipefail

fail=0
for f in "$@"; do
  declare -A args=() stages=()
  while read -r word rest; do
    case ${word^^} in
      ARG)
        # ARG NAME=default; an ARG without a default has none to check.
        [[ $rest == *=* ]] && args[${rest%%=*}]=${rest#*=}
        ;;
      FROM)
        read -ra parts <<<"$rest"
        # Drop the flags (--platform=...) before the image.
        while [[ ${parts[0]} == --* ]]; do parts=("${parts[@]:1}"); done
        image=${parts[0]}
        # The stage this FROM starts, for a later FROM to name.
        if [ ${#parts[@]} -ge 3 ] && [ "${parts[1],,}" = as ]; then stage=${parts[2]}; else stage=; fi
        if [ -n "${stages[$image]:-}" ]; then
          [ -n "$stage" ] && stages[$stage]=1
          continue
        fi
        resolved=$image
        if [[ $image =~ ^\$\{?([A-Za-z_][A-Za-z0-9_]*)\}?$ ]]; then
          resolved=${args[${BASH_REMATCH[1]}]:-}
        fi
        if ! [[ $resolved =~ ^[^@]+:[^@]+@sha256:[0-9a-f]{64}$ ]]; then
          shown=$image
          [ "$resolved" != "$image" ] && shown+=" (${resolved:-no default})"
          echo "$f: FROM $shown is not pinned as name:tag@sha256:digest" >&2
          fail=1
        fi
        [ -n "$stage" ] && stages[$stage]=1
        ;;
    esac
  done <"$f"
  unset args stages
done
exit "$fail"
