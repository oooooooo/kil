# kil

Currently, to `kill -9` a process, I run `ps | grep` and then `kill -9` by hand.

I want a `kil` command that kills processes by PID or by matching string.

Ideally:

```sh
kil <string>
```

filters the process list in real time, and I can kill every process shown on
screen at once.

Using fzf is fine, and so is writing it from scratch in Go.

## Decisions

- Killing everything shown is done with `Alt-A` (select all shown; pressed
  again when all are selected, it deselects them) followed by `Enter`, not
  with `Enter` alone. `Enter` only signals the selected lines, or
  the cursor line when nothing is selected, so that pressing `Enter` mid-filter
  cannot kill a large, unintended set of processes.
- Selected processes are pinned to the top of the list and stay on screen when
  the query no longer matches them. fzf keeps such selections hidden and still
  prints them on `Enter`, so the fzf-based version cleared every selection on a
  query change instead, which made selecting across several queries
  impossible. Pinning keeps both: every process `Enter` signals is on screen,
  and selections survive query changes. This is why kil is written in Go
  rather than wrapping fzf.
- Matching uses fzf's own algorithm (`github.com/junegunn/fzf/src/algo`), so
  queries behave as in fzf. That package is not a stable API, so the fzf
  version is pinned in `go.mod`.
- The query is edited with readline's keys, so `Ctrl-A` moves to the start of
  the query and `Ctrl-K` deletes to its end, as in a shell. Select-all
  therefore moved from `Ctrl-A` to `Alt-A`, and moving up is `Up` / `Ctrl-P`
  rather than fzf's `Ctrl-K`.
- Matching is fuzzy by default, as in fzf. Exact matching, as fzf `--exact`,
  is available with `-e`, `KIL_EXACT=1`, or `Ctrl-X`, because fuzzy matches on
  long command lines (e.g. `node` matching `polkitd --no-debug`) bury
  the processes the user means.
- Pinned selections are ordered by when they were selected, so `Alt-A`, which
  selects in the order shown, does not reorder the list.
- The PID, PPID, USER, and COMM columns never scroll away, as with fzf
  `--freeze-left=4`. fzf's horizontal scrolling alone hid them when a match
  was far right, so the user could not tell which process a row was.
- `Ctrl-R` keeps selections whose processes exited or changed, pinned until
  deselected, and `Enter` reports them as skipped. Dropping them instead left
  nothing selected, so `Enter` signalled the cursor line, a process the user
  never chose.
- The header names only `Tab`, `Alt-A`, `Enter` with its signal, and `Alt-H`,
  and `Alt-H` shows every key in place of the list. The full key list in one
  line was cut on narrow terminals before the keys that matter. While the help
  is shown, any key other than `Ctrl-C` only closes it, so `Enter` cannot
  signal a row the user cannot see, and `Esc` returns to the list rather than
  leaving kil.
- `Enter` signals only selected rows drawn on screen. If other selections
  are off screen (scrolled away, pushed off by pinning, or hidden by a
  resize), it signals the rows on screen and keeps kil open with the rest
  brought on screen, so the user sees them before pressing `Enter` again.
  Signalling them unseen, or dropping them silently, were both rejected.
  `Esc` after such a partial `Enter` asks `[Y/n]` before leaving the rest;
  `Ctrl-C` aborts without asking. Bubble Tea draws asynchronously, so an
  `Enter` typed right away could signal rows not yet on the terminal. `Enter`
  is therefore ignored for 300 ms after the help closes, and while a selected
  row on screen came on screen less than 300 ms ago, however it came (a
  partial `Enter`, scrolling, a resize, a query edit, or starting kil) and
  whenever it was selected. Each row's wait is its own, so rows that keep
  coming on screen do not hold `Enter` for rows shown long before. Waiting
  only after a partial `Enter`, or only for rows already selected, still let
  quick keys signal unseen rows, such as `Up`, `Alt-A`, `Enter`. The cursor
  row alone does not wait, so typing a query and pressing `Enter` at once
  works as in fzf. Keys arriving after the outcome is decided are ignored,
  because Bubble Tea delivers them until it quits, and `Ctrl-C` then `Enter`
  signalled rows. `Alt-A` likewise selects only the rows on screen and then
  scrolls to keep the rows it selected there.
- The check before signalling compares the ps line without the PPID. Killing
  a parent reparents its children, so with the PPID a child selected after
  its parent was skipped as changed and survived.
