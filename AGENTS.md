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

- Killing everything shown is done with `Ctrl-A` (select all shown) followed by
  `Enter`, not with `Enter` alone. `Enter` only signals the selected lines, or
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
