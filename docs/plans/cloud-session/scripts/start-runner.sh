#!/bin/bash
# Starts the host Gitea Actions runner as user ci, detached, with a clean
# environment (root's npm and CA settings point into /root, which ci can't read).
pgrep -u ci -x gitea-runner >/dev/null && { echo "gitea-runner already runs"; exit 0; }
setsid nohup runuser -u ci -- env -i HOME=/home/ci USER=ci LOGNAME=ci LANG=C.UTF-8 \
  PATH=/home/ci/node/bin:/home/ci/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin \
  bash -c 'cd /home/ci/runner && exec gitea-runner daemon --config config.yaml >> daemon.log 2>&1' > /dev/null 2>&1 < /dev/null &
