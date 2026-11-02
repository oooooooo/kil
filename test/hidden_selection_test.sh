#!/usr/bin/env bash
# Drives kil through tmux, because the behaviour under test only exists in
# fzf's interactive key handling, which fzf's --filter mode cannot exercise.
# kil runs with KIL_SIGNAL=0, which signals nothing, so a test that sends a
# key at the wrong moment cannot terminate a real process on the machine;
# what kil would have signalled is read from its "sent signal 0:" output.
set -euo pipefail

cd "$(dirname "$0")/.."
KIL="$PWD/kil"
ID="kiltest$$"
SESSION="kil-test-$$"
OUT=$(mktemp)
pids=()

cleanup() {
  tmux kill-session -t "$SESSION" 2>/dev/null || true
  for p in "${pids[@]}"; do kill -9 "$p" 2>/dev/null || true; done
  rm -f "$OUT"
}
trap cleanup EXIT

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
# sent while fzf is still applying the previous ones.
wait_until() {
  for _ in $(seq 50); do
    "$@" && return 0
    sleep 0.1
  done
  screen >&2
  fail "timed out waiting for: $*"
}

shows() { screen | grep -q -- "$1"; }
hides() { ! screen | grep -q -- "$1"; }

wait_for_kil_exit() {
  wait_until grep -q -- "kil finished" "$OUT"
}

test_selection_hidden_by_filter_is_not_signalled() {
  spawn "$ID-alpha"
  spawn "$ID-beta"

  # The queries are typed rather than passed as arguments, so that only the
  # sleeps contain them: kil's process list is taken at start, and the tmux
  # and shell command lines that start kil would otherwise match too. The
  # leading ' makes fzf match exactly instead of fuzzily.
  tmux new-session -d -s "$SESSION" -x 200 -y 40 \
    "KIL_SIGNAL=0 '$KIL' >'$OUT' 2>&1; echo 'kil finished' >>'$OUT'"
  wait_until shows " (0) "
  tmux send-keys -t "$SESSION" -l "'$ID-alpha"
  wait_until shows " 1/[0-9]* (0) "

  # Select alpha, then change the query so that only beta is shown.
  tmux send-keys -t "$SESSION" Tab
  wait_until shows " (1) "
  tmux send-keys -t "$SESSION" C-u
  tmux send-keys -t "$SESSION" -l "'$ID-beta"
  wait_until shows "$ID-beta 600"
  wait_until hides "$ID-alpha 600"
  tmux send-keys -t "$SESSION" Enter
  wait_for_kil_exit

  local errors=()
  grep -q -- "sent signal 0: .*$ID-beta 600" "$OUT" ||
    errors+=("beta (shown, under the cursor) was not signalled")
  ! grep -q -- "sent signal 0: .*$ID-alpha 600" "$OUT" ||
    errors+=("alpha (selected, then hidden by the filter) was signalled")
  if [ ${#errors[@]} -gt 0 ]; then
    cat "$OUT" >&2
    fail "$(printf '%s; ' "${errors[@]}")"
  fi
  echo "ok: selection hidden by the filter is not signalled"
}

test_selection_hidden_by_filter_is_not_signalled
