#!/usr/bin/env bash
# Factory evaluator: runs one headless Claude session that follows the
# factory-evaluator skill (.claude/skills/factory-evaluator/SKILL.md). It looks
# at how the factory has run since the last evaluation, reports as a comment on
# the pinned "Factory evaluations" GitHub issue, and files up to five
# improvement issues. Read-only towards the factory.
#
#   hack/evaluate.sh       run one evaluation now (make evaluate does the same)
#
# The relay calls it when its backlog is empty, at most every 6 hours; this
# script does not rate-limit itself. It exits 0 when the session posted its
# evaluation.
#
# Log: .relay/evaluate.log, one line per run. The session's JSON result, stderr
# and closing account are .relay/evaluate-<time>.{json,err,md}; the facts it
# gathered are in .relay/evaluate-<time>/. Open a whole session with
# `claude --resume <id>`.
#
# Env: EVAL_MODEL (default opus, as hack/relay.sh; never Fable), EVAL_TIMEOUT
# (default 1h), EVAL_MAX_BUDGET_USD (default 10), RELAY_DIR (default: the main
# checkout's .relay, also from a worktree).
set -euo pipefail

REPO=$(cd "$(dirname "$0")/.." && pwd)
cd "$REPO"
if [ -z "${RELAY_DIR:-}" ]; then
  RELAY_DIR=$(dirname "$(git rev-parse --path-format=absolute --git-common-dir)")/.relay
fi
LOG=$RELAY_DIR/evaluate.log
MODEL=${EVAL_MODEL:-opus}
TIMEOUT=${EVAL_TIMEOUT:-1h}
BUDGET=${EVAL_MAX_BUDGET_USD:-10}

case "${MODEL,,}" in
  *fable*) echo "evaluate.sh: never the Fable model; set EVAL_MODEL to opus or sonnet" >&2; exit 2 ;;
esac

mkdir -p "$RELAY_DIR"
[ -e "$RELAY_DIR/.gitignore" ] || echo '*' >"$RELAY_DIR/.gitignore"
log() { echo "$(date -Is) $*" >>"$LOG"; }

exec 8>"$RELAY_DIR/evaluate.lock"
if ! flock -n 8; then
  log "evaluation skipped: another one is running"
  echo "another evaluation is running; see $LOG" >&2
  exit 1
fi

# The session's gh reads GH_TOKEN; it is never printed.
if [ -f "$REPO/hack/lib/github.sh" ]; then
  # shellcheck source=/dev/null
  . "$REPO/hack/lib/github.sh"
fi
if [ -z "${GH_TOKEN:-}" ]; then
  GH_TOKEN=$(printf 'protocol=https\nhost=github.com\n\n' | git credential fill 2>/dev/null | sed -n 's/^password=//p') || true
fi
if [ -z "${GH_TOKEN:-}" ]; then
  log "evaluation failed: no GitHub token from the git credential helper"
  echo "no GitHub token; see $LOG" >&2
  exit 1
fi
export GH_TOKEN

out=$RELAY_DIR/evaluate-$(date +%Y%m%d-%H%M%S)
id=$(cat /proc/sys/kernel/random/uuid)

read -r -d '' PROMPT <<EOF || true
You run headless, started by hack/evaluate.sh; no person is watching and nobody
can answer a question. Evaluate the factory: read
.claude/skills/factory-evaluator/SKILL.md and follow it.

Inputs:
- EVAL_DATA=$out : gather the facts into this directory.
- RELAY_DIR=$RELAY_DIR : the relay's log and session accounts.
- GH_TOKEN is set in your environment and gh uses it. Never print it.

Rules for this headless run:
- Never use the Fable model, for anything. Start no subagents.
- Read-only towards the factory and the repository: no kubectl changes, no
  Gitea writes, no file edits outside EVAL_DATA, no commits. Your only writes
  are the GitHub labels, issues and comments the skill describes.
- If an action is refused, don't retry it; note it in the evaluation comment.
- End your turn with the three lines the skill's last step names.
EOF

log "evaluation start: id $id, model $MODEL, HEAD $(git rev-parse --short HEAD)"
echo "evaluation started (session $id); log: $LOG"
t0=$(date +%s)
rc=0
timeout "$TIMEOUT" claude -p --permission-mode auto --model "$MODEL" \
  --session-id "$id" --output-format json --max-budget-usd "$BUDGET" \
  --add-dir "$RELAY_DIR" "$PROMPT" >"$out.json" 2>"$out.err" || rc=$?
mins=$((($(date +%s) - t0) / 60))

if [ -s "$out.json" ] && jq -e .result "$out.json" >/dev/null 2>&1; then
  jq -r .result "$out.json" >"$out.md"
  stats=$(jq -r '"\(.num_turns) turns, \((.duration_ms // 0) / 60000 | floor)m, $\((.total_cost_usd // 0) * 100 | round / 100), error \(.is_error)"' "$out.json")
  url=$(sed -n 's/^EVALUATION: *//p' "$out.md" | tail -1)
  filed=$(sed -n 's/^FILED: *//p' "$out.md" | tail -1)
  commented=$(sed -n 's/^COMMENTED: *//p' "$out.md" | tail -1)
  if [ "$rc" = 0 ] && [ "$(jq -r .is_error "$out.json")" = false ] && [[ "$url" == https://* ]]; then
    log "evaluation ok: $stats; comment $url; filed ${filed:-none}; commented ${commented:-none}; id $id"
    echo "evaluation posted: $url"
    exit 0
  fi
  log "evaluation failed: exit $rc, $stats, no comment URL in $out.md; id $id"
else
  log "evaluation failed: exit $rc after ${mins}m, no result; see $out.err; id $id"
fi
[ "$rc" = 124 ] && log "evaluation hit the $TIMEOUT timeout"
echo "evaluation failed; see $LOG" >&2
exit 1
