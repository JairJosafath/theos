#!/bin/bash
# Bring the cloud session's factory back after the machine's processes are gone
# (the container itself survived): Docker, the proxy relay for the Kind node,
# the Kind node, Gitea's port-forward, the CI runner. Safe to run again.
# Adapted from docs/plans/cloud-session/restore.sh (differences in STATE.md).
W=${THEOS_SESSION_DIR:?set THEOS_SESSION_DIR to the folder this session keeps its factory in}
export PATH=$W/bin:$PATH
P=$(echo "$HTTPS_PROXY" | sed -E 's#.*:([0-9]+)/?$#\1#')
[ -n "$P" ] || { echo "HTTPS_PROXY is not set: run this from a session shell" >&2; exit 1; }

# 1. Docker.
pgrep -x dockerd >/dev/null || (setsid nohup dockerd >> $W/logs/dockerd.log 2>&1 < /dev/null &)
for i in $(seq 1 60); do docker info >/dev/null 2>&1 && break; sleep 1; done
docker info >/dev/null 2>&1 || { echo "dockerd did not start: see $W/logs/dockerd.log" >&2; exit 1; }

# 2. The relay of the session's proxy on the kind network's gateway. The
# session's proxy port changes with the session, so a relay on an old port
# is replaced.
GW=$(docker network inspect kind -f '{{(index .IPAM.Config 0).Gateway}}' 2>/dev/null)
[ -n "$GW" ] || { echo "no docker network 'kind': the cluster is gone, rebuild it (STATE.md)" >&2; exit 1; }
if ! pgrep -f "proxy-relay.py $GW 3128 $P\$" >/dev/null; then
  pkill -f proxy-relay.py 2>/dev/null
  (setsid nohup python3 $W/bin/proxy-relay.py "$GW" 3128 "$P" >> $W/logs/relay.log 2>&1 < /dev/null &)
fi

# 3. The Kind node (a stopped container after a dockerd restart).
[ "$(docker inspect -f '{{.State.Running}}' theos-control-plane 2>/dev/null)" = true ] || docker start theos-control-plane >/dev/null
grep -q 'gitea.gitea.svc.cluster.local' /etc/hosts || echo '127.0.0.1 gitea.gitea.svc.cluster.local' >> /etc/hosts
[ -s ~/.kube/config ] || { mkdir -p ~/.kube && cp $W/kubeconfig ~/.kube/config && chmod 600 ~/.kube/config; }
for i in $(seq 1 60); do kubectl --context kind-theos get nodes >/dev/null 2>&1 && break; sleep 3; done
for i in $(seq 1 90); do kubectl --context kind-theos -n gitea get pods --no-headers 2>/dev/null | grep '^gitea-' | grep -q '1/1 *Running' && break; sleep 4; done

# 4. Gitea on the host: http://gitea.gitea.svc.cluster.local:3000 (127.0.0.1 only).
# A loop, since kubectl port-forward ends when the Gitea pod restarts.
pgrep -f 'scripts/gitea-forward.sh' >/dev/null || \
  (setsid nohup $W/scripts/gitea-forward.sh >> $W/logs/pf3000.log 2>&1 < /dev/null &)

# 5. The CI runner, on the session's current proxy port.
sed -i -E "s#http://127\.0\.0\.1:[0-9]+#http://127.0.0.1:$P#g" /home/ci/runner/config.yaml
$W/scripts/start-runner.sh

sleep 5
curl -s --noproxy '*' --max-time 10 -o /dev/null -w 'gitea %{http_code}\n' http://gitea.gitea.svc.cluster.local:3000/api/v1/version
kubectl --context kind-theos get nodes --no-headers 2>&1 | cut -c1-80
pgrep -af 'dockerd|proxy-relay|gitea-forward|port-forward|gitea-runner daemon' | cut -c1-90
