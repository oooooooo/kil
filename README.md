# kil

Interactively find and kill processes with [fzf](https://github.com/junegunn/fzf).

Instead of the usual `ps | grep <name>` followed by `kill -9 <pid>`, just run:

```sh
kil <query>
```

The process list is filtered in real time as you type. Press **Enter** and the selected processes are killed.

## Requirements

- [fzf](https://github.com/junegunn/fzf)
- `bash`, `ps`, `awk` (available on any Linux/macOS system)

## Installation

Copy the script somewhere on your `PATH` and make it executable:

```sh
install -m 755 kil ~/.local/bin/kil
```

## Usage

```sh
kil            # browse all processes
kil node       # start with "node" as the initial query
kil node dev   # arguments are joined into one query: "node dev"
kil -- -zsh    # use -- when the query starts with "-"
kil --help     # show usage
```

Inside the fzf window:

| Key | Action |
| --- | --- |
| type | Filter the process list in real time |
| `Tab` | Select multiple processes (optional) |
| `Ctrl-R` | Refresh the process list, keeping your query and `Tab` selections (selections are kept only if your fzf supports `--id-nth`) |
| `Enter` | Kill the selected processes, or the line under the cursor if none are selected |
| `Ctrl-C` / `Esc` | Abort without killing anything |

Each result is reported as `sent SIGKILL:` (or whichever signal you chose), `skipped:`, or `failed:` (e.g. no permission).

### Exit status

| Status | Meaning |
| --- | --- |
| `0` | Every selected process was signalled |
| `1` | Nothing was selected, or some processes were skipped or failed |
| `2` | Usage error, invalid `KIL_SIGNAL`, or fzf failed (e.g. no terminal) |
| `130` | Aborted with `Ctrl-C` / `Esc` |

## Configuration

| Environment variable | Default | Description |
| --- | --- | --- |
| `KIL_SIGNAL` | `9` (SIGKILL) | Signal number or name to send, e.g. `KIL_SIGNAL=15 kil` or `KIL_SIGNAL=TERM kil`. Invalid values are rejected. |

## Notes

- The default signal is `SIGKILL` (`kill -9`), which terminates processes immediately without giving them a chance to clean up. Use `KIL_SIGNAL=15` for a graceful shutdown.
- `kil` hides only its own processes (itself, the `fzf` it started, and their children). Other `fzf` or `kil` sessions stay in the list and can be killed, and so do the shell you started `kil` from and `sudo`; be careful not to select those.
- The process list is a snapshot taken when `kil` starts; press `Ctrl-R` to refresh it if processes have appeared, exited, or changed while you were filtering.
- Before sending a signal, `kil` re-checks that each selected PID still shows the same line in `ps`, and reports it as `skipped:` otherwise. This is best effort: it cannot tell apart a restarted process with the identical command line, and the PID can still change between the check and the signal.
- `kil` ignores `FZF_DEFAULT_OPTS` and `FZF_DEFAULT_OPTS_FILE`, because options such as `--read0`, `--filter`, or `--print-query` change what fzf outputs, and `kil` treats that output as PIDs. Your usual fzf colors and key bindings therefore do not apply inside `kil`.

## License

[MIT](LICENSE)
