#!/bin/bash
# Bring the cloud session's factory back after the machine restarts:
# Docker, the proxy relay for the Kind node, Gitea's port-forwards, the CI runner.
S=/tmp/claude-0/-home-user/18719ebf-d36b-559b-9313-159b9aea66d8/scratchpad
P=$(echo "$HTTPS_PROXY" | sed -E 's#.*:([0-9]+)/?$#\1#')
pgrep -x dockerd >/dev/null || (nohup dockerd > $S/dockerd.log 2>&1 &)
for i in $(seq 1 30); do docker info >/dev/null 2>&1 && break; sleep 1; done
pgrep -f proxy-relay.py >/dev/null || (nohup python3 $S/bin/proxy-relay.py 172.18.0.1 3128 "$P" > $S/relay.log 2>&1 &)
grep -q 'gitea.gitea.svc.cluster.local' /etc/hosts || echo '127.0.0.1 gitea.gitea.svc.cluster.local' >> /etc/hosts
for i in $(seq 1 60); do kubectl --context kind-theos get nodes >/dev/null 2>&1 && break; sleep 3; done
for i in $(seq 1 60); do kubectl --context kind-theos -n gitea get pods --no-headers 2>/dev/null | grep '^gitea-' | grep -q '1/1 *Running' && break; sleep 4; done
pgrep -f 'port-forward svc/gitea 3917' >/dev/null || (nohup kubectl --context kind-theos -n gitea port-forward svc/gitea 3917:3000 > $S/pf.log 2>&1 &)
pgrep -f 'port-forward --address 127.0.0.1 svc/gitea 3000' >/dev/null || (nohup kubectl --context kind-theos -n gitea port-forward --address 127.0.0.1 svc/gitea 3000:3000 > $S/pf3000.log 2>&1 &)
sed -i -E "s#http://127\.0\.0\.1:[0-9]+#http://127.0.0.1:$P#g" /home/ci/runner/config.yaml
pgrep -x gitea-runner >/dev/null || runuser -u ci -- bash -c 'cd /home/ci/runner && nohup gitea-runner daemon --config config.yaml > daemon.log 2>&1 < /dev/null &' > /dev/null 2>&1 < /dev/null
sleep 5
curl -s --noproxy '*' --max-time 10 -o /dev/null -w 'gitea %{http_code}\n' http://127.0.0.1:3000/api/v1/version
pgrep -af 'dockerd|proxy-relay|port-forward|gitea-runner' | cut -c1-70
