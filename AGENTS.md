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
- Changing the query clears every selection. fzf otherwise keeps selections that
  the new query hides and prints them on `Enter`, so processes that are no
  longer on screen would be killed. This gives up selecting across several
  queries in one session.
