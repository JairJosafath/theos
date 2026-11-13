#!/usr/bin/env bash
# Tests for hack/quickstart.sh (#53), with docker, kind, kubectl, helm and
# curl faked: it follows the guide's install step for step, on a kubeconfig of
# its own and never the user's; it asks for the release's chart before it
# makes anything, and says why when it can't pull it; it uses a cluster that
# exists; with
# GHCR_USER and GHCR_TOKEN it logs in and names the pull secret; --down
# removes the cluster; --dry-run runs nothing; a download whose sha256 isn't
# the pinned one installs nothing; and what it pins is what the repository
# pins: the chart's version, the guide's Agent Sandbox, the Makefile's Helm,
# the sample's Brief.
# Run: hack/quickstart_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
ROOT=$(cd "$(dirname "$0")/.." && pwd)
QUICK=$ROOT/hack/quickstart.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

# The fakes log each call as "<tool> <args>" in $T/log; kubectl keeps what
# apply -f - read in $T/applied, and kind lists the clusters in $T/clusters.
mkdir -p "$T/fakes"
for tool in docker curl; do
  printf '#!/usr/bin/env bash\necho "%s $*" >>"%s/log"\n' "$tool" "$T" >"$T/fakes/$tool"
done
# helm reads what it is piped, as helm registry login --password-stdin does.
# A stand-in that exits without reading leaves the script's printf writing to
# a closed pipe once in a thousand runs, more under load: printf dies of
# SIGPIPE, pipefail fails the pipeline, and the script stops after the login,
# before the pull secrets and the release (seen once in a busy make check).
# With $T/nochart there, it can't pull the chart, in GHCR's words.
cat >"$T/fakes/helm" <<FAKE
#!/usr/bin/env bash
echo "helm \$*" >>"$T/log"
case " \$* " in
  *" --password-stdin "*) cat >/dev/null ;;
  *" show chart "*)
    if [ -e "$T/nochart" ]; then
      echo "Error: GET https://ghcr.io/v2/: DENIED: requested access to the resource is denied" >&2
      exit 1
    fi
    ;;
esac
exit 0
FAKE
cat >"$T/fakes/kind" <<FAKE
#!/usr/bin/env bash
echo "kind \$*" >>"$T/log"
[ "\$1 \$2" = "get clusters" ] && cat "$T/clusters" 2>/dev/null
exit 0
FAKE
cat >"$T/fakes/kubectl" <<FAKE
#!/usr/bin/env bash
echo "kubectl \$*" >>"$T/log"
case " \$* " in
  *" --dry-run=client "*) echo "kind: Fake" ;;
  *" apply -f - "*) cat >>"$T/applied" ;;
esac
exit 0
FAKE
chmod +x "$T/fakes"/*

# quick ARGS...: the script with the fakes first on PATH and a home of its own.
quick() {
  : >"$T/log"
  : >"$T/applied"
  env PATH="$T/fakes:$PATH" HOME="$T/userhome" THEOS_HOME="$T/home" GHCR_USER="${GHCR_USER:-}" \
    GHCR_TOKEN="${GHCR_TOKEN:-}" "$QUICK" "$@" >"$T/out" 2>&1
}
# forwarded PORT: waits until the UI's port-forward, which the script leaves
# running in the background, has been logged; the script returns before it,
# and a line it logged late would land in the next run's log.
forwarded() {
  local n=0
  until grep -q "port-forward svc/theos-ui $1:8080$" "$T/log" || [ $n -ge 50 ]; do
    sleep 0.1
    n=$((n + 1))
  done
}
kubeconfig="--kubeconfig $T/home/kubeconfig"
chart_version=$(sed -n 's/^version: *//p' "$ROOT/dist/chart/Chart.yaml")

quick
check "up exits 0" "$?" 0
forwarded 8080
check "it asks for the chart first, before anything is made (#53)" \
  "$(grep -v '^docker info$' "$T/log" | sed -n 1p)" \
  "helm $kubeconfig show chart oci://ghcr.io/jairjosafath/theos/charts/theos --version $chart_version"
check "it makes a cluster of its own, with its own kubeconfig" \
  "$(grep -c "^kind create cluster --name theos-quickstart $kubeconfig --wait 120s$" "$T/log")" 1
check "it installs Agent Sandbox at the guide's version" \
  "$(grep -c "^kubectl $kubeconfig apply -f https://github.com/kubernetes-sigs/agent-sandbox/releases/download/v1.0.4/sandbox.yaml$" "$T/log")" 1
check "and turns its leader election off, as the guide does" \
  "$(grep -c "^kubectl $kubeconfig -n agent-sandbox-system patch deployment agent-sandbox-controller .*--leader-elect=false" "$T/log")" 1
check "it makes the release's and Gitea's namespaces" \
  "$(grep -c "^kubectl $kubeconfig create namespace \(theos-system\|gitea\) --dry-run=client -o yaml$" "$T/log")" 2
check "it installs the chart's own version from GHCR, with the project" \
  "$(grep -c "^helm $kubeconfig upgrade --install theos oci://ghcr.io/jairjosafath/theos/charts/theos --version $chart_version -n theos-system --wait --timeout 15m --set gitea.createNamespace=false,gitea.projects={webshop}$" "$T/log")" 1
check "it waits for the Brief's plan" \
  "$(grep -c "^kubectl $kubeconfig -n webshop wait brief/stand-in-demo --for=jsonpath={.status.phase}=Planned --timeout=15m$" "$T/log")" 1
check "it forwards the UI" "$(grep -c "^kubectl $kubeconfig -n theos-system port-forward svc/theos-ui 8080:8080$" "$T/log")" 1
check "every kubectl and helm call names its own kubeconfig" \
  "$(grep '^\(kubectl\|helm\) ' "$T/log" | grep -vc -- "$kubeconfig")" 0
check "no login and no pull secret without GHCR_USER" "$(grep -c 'registry login\|docker-registry' "$T/log")" 0
check "it says where the UI is" "$(grep -c 'http://localhost:8080/?area=work' "$T/out")" 1
check "and how to remove it" "$(grep -c -- '--down' "$T/out")" 1

# The Brief it applies is the sample's, comments aside.
sample=$(grep -v '^#' "$ROOT/config/samples/brief/stand-in.yaml")
check "the Brief is config/samples/brief/stand-in.yaml" "$(grep -v '^kind: Fake$' "$T/applied")" "$sample"

echo theos-quickstart >"$T/clusters"
quick
forwarded 8080
check "a cluster that exists is used, not made again" "$(grep -c '^kind create cluster' "$T/log")" 0
check "and its kubeconfig is written" \
  "$(grep -c "^kind export kubeconfig --name theos-quickstart $kubeconfig$" "$T/log")" 1
rm -f "$T/clusters"

GHCR_USER=ada GHCR_TOKEN=t0ken quick
forwarded 8080
check "with GHCR_USER it logs helm in" "$(grep -c "^helm $kubeconfig registry login ghcr.io -u ada --password-stdin$" "$T/log")" 1
check "makes the pull secret in both namespaces" "$(grep -c 'create secret docker-registry ghcr .*--docker-username=ada' "$T/log")" 2
check "and names it in the release" \
  "$(grep -c -- '--set gitea.createNamespace=false,gitea.projects={webshop},global.imagePullSecrets\[0\].name=ghcr,manager.imagePullSecrets\[0\].name=ghcr$' "$T/log")" 1
check "the token is in no output" "$(grep -c t0ken "$T/out")" 0
check "the login comes before it asks for the chart" \
  "$(grep -v '^docker info$' "$T/log" | sed -n 1,2p | cut -d' ' -f4-5 | tr '\n' ' ')" "registry login show chart "

# #53: a release that isn't published, or packages that are private. The
# script says so before it makes anything, with helm's words and its own.
touch "$T/nochart"
quick
check "a chart that can't be pulled ends the script" "$?" 1
check "nothing was made" "$(grep -vc '^docker info$\|^helm .* show chart ' "$T/log")" 0
check "it shows helm's words" "$(grep -c 'DENIED: requested access to the resource is denied' "$T/out")" 1
check "and says the two reasons and what to do" \
  "$(grep -c "^quickstart: can't pull the chart of theos $chart_version from ghcr.io/jairjosafath/theos, and nothing was made. Either that release isn't published there (THEOS_VERSION picks another), or its packages are private: then GHCR_USER and GHCR_TOKEN" "$T/out")" 1
rm -f "$T/nochart"

env THEOS_PROJECT=shop THEOS_UI_PORT=9090 THEOS_VERSION=9.9.9 PATH="$T/fakes:$PATH" THEOS_HOME="$T/home" "$QUICK" >"$T/out" 2>&1
forwarded 9090
check "THEOS_PROJECT, THEOS_VERSION and THEOS_UI_PORT are used" \
  "$(grep -c -- '--version 9.9.9 .*gitea.projects={shop}$\|-n shop wait brief\|port-forward svc/theos-ui 9090:8080$' "$T/log")" 3

quick --down
check "--down removes the cluster" "$(cat "$T/log")" "kind delete cluster --name theos-quickstart"
check "and its kubeconfig" "$([ -e "$T/home/kubeconfig" ] && echo there)" ""

quick --dry-run
check "--dry-run exits 0" "$?" 0
check "--dry-run runs nothing" "$(grep -vc '^$' "$T/log")" 0
check "and prints the install" "$(grep -c '^+ .*upgrade --install theos oci://' "$T/out")" 1
check "and the question for the chart" "$(grep -c '^+ .* show chart oci://ghcr.io/jairjosafath/theos/charts/theos --version ' "$T/out")" 1

quick --nope
check "an unknown argument is refused" "$?" 1

# A download: no kind on PATH, and a curl that writes something else than
# kind. PATH holds only the fakes and the few tools the script itself needs.
mkdir -p "$T/bare" "$T/min"
for tool in bash env cat mkdir rm mv chmod cut grep sed tar uname sha256sum shasum dirname nohup; do
  p=$(command -v "$tool" 2>/dev/null) && ln -sf "$p" "$T/min/$tool"
done
cp "$T/fakes/docker" "$T/fakes/kubectl" "$T/fakes/helm" "$T/bare/"
cat >"$T/bare/curl" <<FAKE
#!/usr/bin/env bash
echo "curl \$*" >>"$T/log"
while [ \$# -gt 0 ]; do [ "\$1" = -o ] && { echo "not kind" >"\$2"; break; }; shift; done
FAKE
chmod +x "$T/bare/curl"
: >"$T/log"
env -i PATH="$T/bare:$T/min" HOME="$T/userhome" THEOS_HOME="$T/dl" "$T/min/bash" "$QUICK" >"$T/out" 2>&1
check "a download with another sha256 fails" "$?" 1
check "it says which sha256 it wanted, and stops at the first tool" \
  "$(grep -c 'kind-.* has sha256 [0-9a-f]\{64\}, not the pinned [0-9a-f]\{64\}; nothing was installed' "$T/out")" 1
check "it went on to no other download" "$(grep -c '^curl ' "$T/log")" 1
check "it downloaded kind at its pinned version" "$(grep -c 'kubernetes-sigs/kind/releases/download/v0.30.0/kind-' "$T/log")" 1
check "nothing was installed" "$(ls "$T/dl/bin" 2>/dev/null | wc -l)" 0
check "and no cluster was made" "$(grep -c 'create cluster' "$T/log")" 0

# What it pins is what the repository pins.
check "its default version is the chart's" "$(sed -n 's/^VERSION=${THEOS_VERSION:-\(.*\)}$/\1/p' "$QUICK")" "$chart_version"
check "its Agent Sandbox is the guide's" \
  "$(grep -c "agent-sandbox/releases/download/$(sed -n 's/^AGENT_SANDBOX_VERSION=//p' "$QUICK")/sandbox.yaml" "$ROOT/docs/getting-started.md")" 1
helm_version=$(sed -n 's/^HELM_VERSION ?= //p' "$ROOT/Makefile")
check "its Helm is the Makefile's" "$(sed -n 's/^HELM_VERSION=//p' "$QUICK")" "$helm_version"
for p in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
  want=$(sed -n "s/^HELM_SHA256_$p = //p" "$ROOT/Makefile")
  check "its Helm sha256 for $p is the Makefile's" "$(grep -c "helm-$p) echo $want ;;" "$QUICK")" 1
done
check "it runs under bash 3.2: no associative array, mapfile or case conversion" \
  "$(grep -c 'declare -A\|mapfile\|readarray\|\${[a-zA-Z_]*,,}\|\${[a-zA-Z_]*^^}' "$QUICK")" 0

exit $fail
