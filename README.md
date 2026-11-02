# kil

Interactively find and kill processes.

Instead of the usual `ps | grep <name>` followed by `kill -9 <pid>`, just run:

```sh
kil <query>
```

The process list is filtered in real time as you type, looking and matching
like [fzf](https://github.com/junegunn/fzf). Press **Enter** and the selected
processes are killed. Selected processes stay on screen even when the query no
longer matches them, so `kil` never kills a process you cannot see.

## Installation

```sh
go install github.com/oooooooo/kil@latest
```

`kil` runs on Linux and macOS and needs only `ps`.

## Usage

```sh
kil            # browse all processes
kil node       # start with "node" as the initial query
kil node dev   # arguments are joined into one query: "node dev"
kil -e ruby    # exact match: only lines that contain "ruby"
kil -- -zsh    # use -- when the query starts with "-"
kil --help     # show usage
```

### Query syntax

The query uses fzf's search syntax. Terms separated by spaces must all match.

| Query | Matches lines that |
| --- | --- |
| `ruby` | contain `r`, `u`, `b`, `y` in this order (fuzzy; also matches `resolve /usr/lib/systemd`) |
| `'ruby` | contain `ruby` |
| `'ruby'` | contain `ruby` as a whole word |
| `.rb$` | end with `.rb` |
| `^ruby` | start with `ruby` (rarely useful: lines start with the PID) |
| `!ruby` | do not contain `ruby` |
| `!'rby` | do not fuzzy-match `rby` |
| `ruby \| node` | match `ruby` or `node` |
| `ruby\ app` | contain `ruby app`, with the space (`\` escapes it) |

A term with an uppercase letter is case sensitive; other terms are not.

With `-e` / `--exact`, `KIL_EXACT=1`, or after pressing `Ctrl-X`, plain terms
match exactly as with fzf `--exact`, and a leading `'` makes a term fuzzy
instead. The info line shows `[exact]` in this mode.

### Keys

| Key | Action |
| --- | --- |
| type | Filter the process list in real time |
| `Up` / `Down` | Move the cursor (also `Ctrl-P` / `Ctrl-N`). As in fzf, the best match is at the bottom |
| `Tab` / `Shift-Tab` | Select or deselect the process under the cursor and move down / up. Selected processes are pinned next to the prompt, so you can select under one query, change the query, and select more |
| `Alt-A` | Select every process currently shown |
| `Ctrl-X` | Switch between fuzzy and exact matching |
| `Ctrl-R` | Refresh the process list, keeping your query and selections |
| `Enter` | Kill the selected processes, or the one under the cursor if none are selected |
| `Ctrl-C` / `Esc` | Abort without killing anything |

The query is edited with readline's keys: `Ctrl-A` / `Ctrl-E` (or `Home` /
`End`) move to the start / end, `Ctrl-B` / `Ctrl-F` (or `Left` / `Right`) and
`Alt-B` / `Alt-F` move by a character and a word, and `Ctrl-U`, `Ctrl-K`,
`Ctrl-W`, `Backspace`, and `Delete` delete to the start, to the end, the
previous word, and a character.

Each result is reported as `sent SIGKILL:` (or whichever signal you chose),
`skipped:`, or `failed:` (e.g. no permission).

### Exit status

| Status | Meaning |
| --- | --- |
| `0` | Every selected process was signalled |
| `1` | Nothing was selected, or some processes were skipped or failed |
| `2` | Usage error, invalid `KIL_SIGNAL` or `KIL_EXACT`, listing processes failed, or no terminal |
| `130` | Aborted with `Ctrl-C` / `Esc` |

## Configuration

| Environment variable | Default | Description |
| --- | --- | --- |
| `KIL_SIGNAL` | `9` (SIGKILL) | Signal number or name to send, e.g. `KIL_SIGNAL=15 kil` or `KIL_SIGNAL=TERM kil`. Invalid values, and numbers without a name such as Linux real-time signals, are rejected. |
| `KIL_EXACT` | unset (fuzzy) | Set to `1` to start in exact mode, as with `-e`. |

## Notes

- The default signal is `SIGKILL` (`kill -9`), which terminates processes
  immediately without giving them a chance to clean up. Use `KIL_SIGNAL=15` for
  a graceful shutdown.
- `kil` hides only itself and its children. The shell you started `kil` from,
  other `kil` sessions, and `sudo` stay in the list; be careful not to select
  those.
- The process list is a snapshot taken when `kil` starts; press `Ctrl-R` to
  refresh it if processes have appeared, exited, or changed.
- Before sending a signal, `kil` re-checks that each selected PID still shows
  the same line in `ps`, and reports it as `skipped:` otherwise. It cannot
  tell apart a restarted process with the identical command line. On Linux,
  the signal goes through a pidfd opened before the check, so it cannot reach
  a different process that reused the PID; on macOS the PID can still change
  between the check and the signal.

## License

[MIT](LICENSE). Matching uses fzf's algorithm, also under the
[MIT License](https://github.com/junegunn/fzf/blob/master/LICENSE).
