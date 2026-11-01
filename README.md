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
```

Inside the fzf window:

| Key | Action |
| --- | --- |
| type | Filter the process list in real time |
| `Tab` | Select multiple processes (optional) |
| `Ctrl-R` | Refresh the process list (keeps your current query/selection) |
| `Enter` | Kill the selected processes, or the line under the cursor if none are selected |
| `Ctrl-C` / `Esc` | Abort without killing anything |

Each result is reported as `killed:` or `failed:` (e.g. no permission, process already gone).

## Configuration

| Environment variable | Default | Description |
| --- | --- | --- |
| `KIL_SIGNAL` | `9` (SIGKILL) | Signal to send. e.g. `KIL_SIGNAL=15 kil` sends SIGTERM |

## Notes

- The default signal is `SIGKILL` (`kill -9`), which terminates processes immediately without giving them a chance to clean up. Use `KIL_SIGNAL=15` for a graceful shutdown.
- `kil` excludes itself and the `fzf` process from the list so you don't kill your own session.
- The process list is a snapshot taken when `kil` starts; press `Ctrl-R` to refresh it if processes have appeared, exited, or changed while you were filtering.
- Before sending a signal, `kil` re-checks each selected PID. If the process has exited or its PID now belongs to a different process, `kil` reports it as `skipped:` instead of killing it.
- `kil` overrides `--print-query`, `--expect`, and `--print0` from `FZF_DEFAULT_OPTS`, so only process lines are ever treated as PIDs.

## License

[MIT](LICENSE)
