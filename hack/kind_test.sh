#!/usr/bin/env bash
# Tests for hack/kind.sh: the checkout tag, the load under the shared name and
# keep-context, with kind, docker and kubectl faked.
# Run: hack/kind_test.sh
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
# shellcheck source=hack/kind.sh
. "$(dirname "$0")/kind.sh"
set +e
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }

fake_kind() {
  echo "kind $*" >>"$T/calls"
  case "$1 $2" in
    "get nodes") [ "$4" = gone ] || printf '%s-control-plane\n' "$4" ;;
    "create cluster") echo "kind-$4" >"$T/context" ;;
  esac
}
fake_docker() { echo "docker $*" >>"$T/calls"; }
fake_kubectl() {
  case "$2" in
    current-context) [ -s "$T/context" ] && cat "$T/context" || { echo "error: current-context is not set" >&2; return 1; } ;;
    use-context) echo "$3" >"$T/context" ;;
    unset) : >"$T/context" ;;
  esac
}
KIND=fake_kind CONTAINER_TOOL=fake_docker KUBECTL=fake_kubectl
calls() { cat "$T/calls" 2>/dev/null; : >"$T/calls"; }

# The tag: two checkouts with one directory name still get their own.
a=$(cd "$T" && mkdir -p x/c4factory y/c4factory && cd x/c4factory && checkout_tag)
b=$(cd "$T/y/c4factory" && checkout_tag)
check "tag: names the directory" "${a%-*}" c4factory
check "tag: differs by path" "$([ "$a" != "$b" ] && echo yes)" yes
check "tag: stable" "$(cd "$T/x/c4factory" && checkout_tag)" "$a"
check "tag: valid docker tag" "$(cd "$T" && mkdir -p 'My Repo' && cd 'My Repo' && checkout_tag | grep -cE '^[a-z0-9_.-]+$')" 1

check "ref: docker hub user" "$(ref c4factory/operator:dev)" docker.io/c4factory/operator:dev
check "ref: library" "$(ref busybox:1)" docker.io/library/busybox:1
check "ref: registry" "$(ref ghcr.io/x/y:1)" ghcr.io/x/y:1
check "ref: registry port" "$(ref localhost:5000/y:1)" localhost:5000/y:1

# Load: the checkout's build goes in, and is named IMAGE in every node.
tag=$(checkout_tag)
load c4factory-2 c4factory/operator:dev >/dev/null
check "load: calls" "$(calls)" "kind load docker-image c4factory/operator:dev-$tag --name c4factory-2
kind get nodes --name c4factory-2
docker exec c4factory-2-control-plane ctr -n k8s.io images tag --force docker.io/c4factory/operator:dev-$tag docker.io/c4factory/operator:dev
docker exec c4factory-2-control-plane ctr -n k8s.io images rm docker.io/c4factory/operator:dev-$tag"
load gone c4factory/operator:dev >/dev/null 2>&1
check "load: no nodes fails" "$?" 1

# keep-context: kind create switches the context; it's put back.
echo kind-c4factory >"$T/context"
keep_context fake_kind create cluster --name c4factory-3
check "keep-context: restores" "$(cat "$T/context")" kind-c4factory
: >"$T/context"
keep_context fake_kind create cluster --name c4factory-3
check "keep-context: stays unset" "$(cat "$T/context")" ""
keep_context false
check "keep-context: keeps the exit code" "$?" 1
calls >/dev/null

check "usage" "$(main bogus 2>/dev/null; echo $?)" 64

exit "$fail"
