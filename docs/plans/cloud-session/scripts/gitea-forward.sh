#!/bin/bash
# Keeps Gitea forwarded to 127.0.0.1:3000: kubectl port-forward ends when the
# Gitea pod restarts, and the CI runner and every git remote use this address.
W=${THEOS_SESSION_DIR:?set THEOS_SESSION_DIR to the folder this session keeps its factory in}
while true; do
  "$W/bin/kubectl" --context kind-theos -n gitea port-forward --address 127.0.0.1 svc/gitea 3000:3000
  sleep 3
done
