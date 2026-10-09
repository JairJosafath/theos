#!/usr/bin/env bash
# Usage: c4-tmp-sweep DIR MINUTES
# Removes what jobs left in DIR (the runner's /tmp, kept from one job to the
# next) more than MINUTES ago, so it doesn't fill up and fail a later job's
# build (#814). It goes by ctime, which a job can't set back the way tar sets
# back mtime, so with MINUTES past the job timeout nothing the running job
# wrote goes. A cache a job only reads (npm's) may lose an entry it wants; the
# job fetches it again. Directories go once they are empty.
set -uo pipefail
dir=${1:?usage: c4-tmp-sweep DIR MINUTES} minutes=${2:?usage: c4-tmp-sweep DIR MINUTES}
find "$dir" -xdev -mindepth 1 -depth -cmin "+$minutes" \
  \( -type d -empty -delete -o ! -type d -delete \) 2>/dev/null
exit 0
