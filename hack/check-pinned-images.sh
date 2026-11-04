#!/usr/bin/env bash
# Checks that every base image of the Dockerfiles is pinned by digest
# (name:tag@sha256:...), so a tag that moves can't change what a release
# builds (#912). A FROM naming an earlier stage passes; a FROM ${VAR} is checked
# through VAR's default from an ARG before the first FROM. A default naming
# theos/... is one of our own toolchain images (hack/agent, hack/runner
# build FROM ${BASE}), which the build always passes and whose own Dockerfile
# is checked here, so it passes (#919). Prints each FROM that isn't pinned and
# exits 1. hack/check-pinned-images_test.sh runs it in make check on every
# Dockerfile Release builds.
#
#   hack/check-pinned-images.sh DOCKERFILE...
set -euo pipefail

fail=0
for f in "$@"; do
  declare -A args=() stages=()
  seen_from=0
  while read -r word rest; do
    case ${word^^} in
      ARG)
        # ARG NAME=default before the first FROM: only those resolve a FROM.
        # An ARG without a default has none to check.
        [ "$seen_from" = 0 ] && [[ $rest == *=* ]] && args[${rest%%=*}]=${rest#*=}
        ;;
      FROM)
        seen_from=1
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
        if [ "$resolved" != "$image" ] && [[ $resolved == theos/* ]]; then
          :
        elif ! [[ $resolved =~ ^[^@]+:[^@]+@sha256:[0-9a-f]{64}$ ]]; then
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
