#!/usr/bin/env bash
# Tests for hack/gh-runner/job-started.sh, the runner's job-started hook: a
# home recorded as the image's, then seeded with an earlier job's logins,
# kubeconfig and leftovers, and a work directory with read-only and unreadable
# directories in its checkout, run as the runner runs a hook (bash -e). After
# the hook, the leftovers are gone, the image's files, the runner and the
# caches stay, and the checkout can be deleted.
# Run: hack/gh-runner/job-started_test.sh
T=$(mktemp -d)
trap 'chmod -R u+rwx "$T" 2>/dev/null; rm -rf "$T"' EXIT
HOOK=$(cd "$(dirname "$0")" && pwd)/job-started.sh
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok   $1"; else echo "FAIL $1: got [$2] want [$3]"; fail=1; fi; }
# hook: the hook as the runner runs a .sh hook.
hook() { bash --noprofile --norc -e -o pipefail "$HOOK" "$@"; }
gone() { if [ -e "$H/$1" ] || [ -L "$H/$1" ]; then echo kept; else echo gone; fi; }

export HOME=$T/home GH_RUNNER_HOME_LIST=$T/etc/home.list
H=$HOME
W=$T/work

# What the image puts there: the runner and its files, the shell's dotfiles,
# the caches' mount point.
mkdir -p "$H/bin" "$H/externals/node20/bin" "$H/.cache" "$W"
touch "$H/.bashrc" "$H/.profile" "$H/config.sh" "$H/run.sh" "$H/bin/Runner.Listener" "$H/externals/node20/bin/node"
hook record
check "record: exit" "$?" 0
check "record: lists the image's files, not the runner's" "$(cat "$GH_RUNNER_HOME_LIST" | tr '\n' ' ')" ".bashrc .profile "

# What the runner writes once it runs: its registration, env, logs, an update.
printf '{"workFolder": "%s"}\n' "$W" >"$H/.runner"
touch "$H/.credentials" "$H/.credentials_rsaparams" "$H/.env" "$H/.path"
mkdir -p "$H/_diag" "$H/bin.2.338.0" "$H/externals.2.338.0"
touch "$H/_diag/Runner_1.log" "$H/bin.2.338.0/Runner.Listener" "$H/new-in-2.338.sh" "$H/.service.template" "$H/run-helper.sh"
mkdir -p "$H/.cache/go-mod" "$H/.cache/make-bin" && touch "$H/.cache/go-mod/m" "$H/.cache/make-bin/kind"

# An earlier job's leftovers.
mkdir -p "$H/.docker" "$H/.config/helm/registry" "$H/.config/gh" "$H/.kube" "$H/stuff/ro" "$T/outside"
echo '{"auths": {"ghcr.io": {"auth": "dead"}}}' >"$H/.docker/config.json"
echo '{"auths": {"ghcr.io": {}}}' >"$H/.config/helm/registry/config.json"
touch "$H/.config/gh/hosts.yml" "$H/.kube/config" "$H/stuff/ro/f" "$T/outside/f"
mkdir -p "$H/.kube/locked" && touch "$H/.kube/locked/f"
chmod 555 "$H/stuff/ro"
chmod 000 "$H/.kube/locked"
ln -s "$T/outside" "$H/link"
echo 'export JOB=1' >>"$H/.bashrc"
mkdir -p "$W/c4factory/c4factory/bin/k8s/ro" "$W/_temp"
mkdir -p "$W/c4factory/c4factory/locked/inner"
touch "$W/c4factory/c4factory/bin/k8s/ro/kube-apiserver" "$W/c4factory/c4factory/locked/inner/f"
chmod 555 "$W/c4factory/c4factory/bin/k8s/ro" "$W/c4factory/c4factory/bin/k8s"
chmod 000 "$W/c4factory/c4factory/locked/inner" "$W/c4factory/c4factory/locked"

out=$(hook 2>&1)
check "hook: exit" "$?" 0
check "hook: no warnings" "$out" ""
check "docker login gone" "$(gone .docker)" gone
check "helm registry login gone" "$(gone .config/helm)" gone
check "gh login gone" "$(gone .config)" gone
check "kubeconfig gone" "$(gone .kube)" gone
check "read-only leftover gone" "$(gone stuff)" gone
check "symlink gone" "$(gone link)" gone
check "symlink's target stays" "$(ls "$T/outside")" f
for p in .bashrc .profile config.sh run.sh bin/Runner.Listener externals/node20/bin/node \
  .runner .credentials .credentials_rsaparams .env .path _diag/Runner_1.log \
  bin.2.338.0/Runner.Listener externals.2.338.0 new-in-2.338.sh .service.template run-helper.sh .cache/go-mod/m .cache/make-bin/kind; do
  check "$p stays" "$(gone "$p")" kept
done
rm -rf "$W/c4factory" 2>/dev/null
check "checkout can be cleaned" "$(ls "$W")" _temp

# GH_RUNNER_WORK names the work directory when there's no .runner.
rm "$H/.runner"
mkdir -p "$T/w2/ro" && chmod 555 "$T/w2/ro"
GH_RUNNER_WORK=$T/w2 hook >/dev/null 2>&1
check "GH_RUNNER_WORK: made writable" "$(stat -c %A "$T/w2/ro" | cut -c3)" w

# Neither .runner nor GH_RUNNER_WORK: the home is still reset.
mkdir -p "$H/.kube" && touch "$H/.kube/config"
hook >/dev/null 2>&1
check "no work directory: exit" "$?" 0
check "no work directory: kubeconfig gone" "$(gone .kube)" gone

# Without the image's list, the home stays and the job is warned.
mkdir -p "$H/.kube" && touch "$H/.kube/config"
out=$(GH_RUNNER_HOME_LIST=$T/none hook 2>&1)
check "no list: exit" "$?" 0
check "no list: kubeconfig stays" "$(gone .kube/config)" kept
check "no list: warns" "$(grep -c '^::warning::.*not reset' <<<"$out")" 1

exit $fail
