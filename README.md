# kil

Interactively find and kill processes.

Instead of the usual `ps | grep <name>` followed by `kill -9 <pid>`, just run:

```sh
kil <query>
```

The process list is filtered in real time as you type. Press **Enter** and the
selected processes are killed. Selected processes stay on screen even when the
query no longer matches them, so `kil` never kills a process you cannot see.

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
kil -- -zsh    # use -- when the query starts with "-"
kil --help     # show usage
```

The query uses [fzf](https://github.com/junegunn/fzf)'s search syntax: terms
separated by spaces must all match, and `node | python` matches either.
`'exact`, `^prefix`, `suffix$`, and `!exclude` work as in fzf.

| Key | Action |
| --- | --- |
| type | Filter the process list in real time |
| `Ctrl-U` | Clear the query |
| `Up` / `Down` | Move the cursor (also `Ctrl-P` / `Ctrl-N`) |
| `Tab` | Select or deselect the process under the cursor. Selected processes are pinned to the top of the list, so you can select under one query, change the query, and select more |
| `Ctrl-A` | Select every process currently shown |
| `Ctrl-R` | Refresh the process list, keeping your query and selections |
| `Enter` | Kill the selected processes, or the one under the cursor if none are selected |
| `Ctrl-C` / `Esc` | Abort without killing anything |

Each result is reported as `sent SIGKILL:` (or whichever signal you chose),
`skipped:`, or `failed:` (e.g. no permission).

### Exit status

| Status | Meaning |
| --- | --- |
| `0` | Every selected process was signalled |
| `1` | Nothing was selected, or some processes were skipped or failed |
| `2` | Usage error, invalid `KIL_SIGNAL`, listing processes failed, or no terminal |
| `130` | Aborted with `Ctrl-C` / `Esc` |

## Configuration

| Environment variable | Default | Description |
| --- | --- | --- |
| `KIL_SIGNAL` | `9` (SIGKILL) | Signal number or name to send, e.g. `KIL_SIGNAL=15 kil` or `KIL_SIGNAL=TERM kil`. Invalid values, and numbers without a name such as Linux real-time signals, are rejected. |

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
