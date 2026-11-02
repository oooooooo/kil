#!/usr/bin/env bash
# Drives the built kil binary through tmux, to check the key handling on a
# real terminal that the Go unit tests can only simulate.
# kil runs with KIL_SIGNAL=0, which signals nothing, so a test that sends a
# key at the wrong moment cannot terminate a real process on the machine;
# what kil would have signalled is read from its "sent signal 0:" output.
set -euo pipefail

cd "$(dirname "$0")/.."
WORK=$(mktemp -d)
KIL="$WORK/kil"
OUT="$WORK/out"
ID="kiltest$$"
SESSION="kil-test-$$"
pids=()

cleanup() {
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  for p in "${pids[@]}"; do kill -9 "$p" 2>/dev/null || true; done
  rm -rf "$WORK"
}
trap cleanup EXIT

go build -o "$KIL" .

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# `exec -a` gives each sleep a unique argv[0], so the test can filter on it in
# kil without matching any other process on the machine.
spawn() {
  bash -c "exec -a '$1' sleep 600" &
  pids+=("$!")
  # Keeps bash from reporting the job as killed during cleanup.
  disown "$!"
}

screen() {
  tmux capture-pane -p -t "$SESSION"
}

# Waits until the screen satisfies the given test command, so keys are not
# sent while kil is still applying the previous ones.
wait_until() {
  for _ in $(seq 50); do
    "$@" && return 0
    sleep 0.1
  done
  screen >&2
  fail "timed out waiting for: $*"
}

shows() { screen | grep -q -- "$1"; }

# The queries are typed rather than passed as arguments, so that only the
# sleeps contain them: kil's process list is taken at start, and the tmux
# and shell command lines that start kil would otherwise match too. The
# leading ' makes the match exact instead of fuzzy.
start_kil() {
  rm -f "$OUT"
  tmux new-session -d -s "$SESSION" -x 200 -y 40 \
    "KIL_SIGNAL=0 '$KIL' >'$OUT' 2>&1; echo 'kil finished' >>'$OUT'"
  wait_until shows "(0 selected)"
}

type_query() {
  tmux send-keys -t "$SESSION" C-u
  tmux send-keys -t "$SESSION" -l "'$1"
  wait_until shows "^> '$1\$"
  wait_until shows "$1 600"
}

finish() {
  tmux send-keys -t "$SESSION" Enter
  wait_until grep -q -- "kil finished" "$OUT"
  tmux kill-session -t "$SESSION" 2>/dev/null || true
}

signalled() { grep -q -- "sent signal 0: .*$1 600" "$OUT"; }

check() {
  local errors=("$@")
  if [ ${#errors[@]} -gt 0 ]; then
    cat "$OUT" >&2
    fail "$(printf '%s; ' "${errors[@]}")"
  fi
}

test_hidden_selection_stays_on_screen() {
  start_kil
  type_query "$ID-alpha"
  tmux send-keys -t "$SESSION" Tab
  wait_until shows "(1 selected)"
  type_query "$ID-beta"

  local errors=()
  shows "● .*$ID-alpha 600" || errors+=("selected alpha is not on screen after the query changed")
  finish
  signalled "$ID-alpha" || errors+=("selected alpha was not signalled")
  ! signalled "$ID-beta" || errors+=("unselected beta was signalled")
  check ${errors[@]+"${errors[@]}"}
  echo "ok: a selection hidden by the query stays on screen and is signalled"
}

test_selecting_across_queries() {
  start_kil
  type_query "$ID-alpha"
  tmux send-keys -t "$SESSION" Tab
  wait_until shows "(1 selected)"
  type_query "$ID-beta"
  # The cursor starts on the pinned alpha row; move to beta and select it.
  tmux send-keys -t "$SESSION" Down Tab
  wait_until shows "(2 selected)"

  local errors=()
  finish
  signalled "$ID-alpha" || errors+=("alpha was not signalled")
  signalled "$ID-beta" || errors+=("beta was not signalled")
  check ${errors[@]+"${errors[@]}"}
  echo "ok: selections made under different queries are all signalled"
}

spawn "$ID-alpha"
spawn "$ID-beta"
test_hidden_selection_stays_on_screen
test_selecting_across_queries
