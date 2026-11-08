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

## Fixing issues

- Once an issue is fixed, and before it is committed, a new agent that knows
  only the issue and the change, not the reasoning behind the fix, researches
  how comparable tools handle the same problem and reviews the change against
  them. What it finds is fixed, test first, and the fixed change goes to
  another new agent in the same way, until a review finds nothing that needs
  fixing; only then is it committed. A review anchored on the fixer's
  reasoning tends to confirm it, other tools show cases the fix missed, and
  fixes made after a review can break what it had checked.

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
- The picker runs on tcell, with kil's own event loop, rather than on Bubble
  Tea. Bubble Tea v1 asked the terminal for its background color as it
  started, even for `--help`, which kept kil waiting 5 seconds on a terminal
  that did not answer and typed a late answer into the query. Bubble Tea v2
  replaced its input reader at each suspend without closing the old one, so
  each `Ctrl-Z` leaked a file descriptor, keys typed around the suspend
  reached the picker out of order, and stopping waited 500 ms for the old
  reader. tcell stops its reader before giving the terminal back and keeps
  what it read, and draws each frame before kil handles the next key. Bubble
  Tea assumed an xterm-like terminal, so without a TERM that tcell knows, or
  with `dumb`, kil uses xterm's definition rather than refuse to start, and
  without TERM or with `dumb` it draws no colors, which such a terminal may
  not show.
- Matching uses fzf's own algorithm (`github.com/junegunn/fzf/src/algo`), so
  queries behave as in fzf. That package is not a stable API, so the fzf
  version is pinned in `go.mod`. kil gives it fzf's working memory, as fzf
  does, so that a match too large for it falls back to fzf's lighter
  algorithm: without it, a long term against a long line took tens of
  megabytes on every keystroke.
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
- A query change puts the cursor on the first match rather than on the
  selections pinned before the matches, and `Tab` does not move the cursor
  from a match onto them. Either left the cursor on a selection, so `Tab`
  right after typing a query, or a second `Tab`, deselected a row chosen under
  an earlier query, which then left the screen. With more selections than fit
  beside the matches, the window follows the cursor and some selections leave
  the screen; `Enter` then signals the ones on screen first, as after any
  scroll.
- The PID, PPID, USER, and COMM columns never scroll away, as with fzf
  `--freeze-left=4`. fzf's horizontal scrolling alone hid them when a match
  was far right, so the user could not tell which process a row was. kil reads
  the command name in a ps run of its own, as the last column, joined with the
  listing by PID, and lays the columns out itself, so that each line says
  where its frozen columns end. A command name with spaces, as `tmux: server`
  or a macOS path, made splitting at whitespace end the columns inside it, and
  one ps run left no reliable boundary. The runs are joined by PID alone:
  killing a parent reparents its children between them, and a PID reused
  between them gives a line that matches neither process, which the check
  before signalling skips. The command name is shown cut to 16 columns, as ps
  on macOS, which gives argv[0], cut it when it was not the last column;
  uncut, one long path pushed every command line off screen. On Linux the user
  column is widened, as procps cut names to 8 characters, as `systemd+`; ps on
  macOS and FreeBSD does not cut them and does not accept a width.
- `Ctrl-R` keeps selections whose processes exited or changed, pinned until
  deselected, and `Enter` reports them as skipped. Dropping them instead left
  nothing selected, so `Enter` signalled the cursor line, a process the user
  never chose.
- The header names only `Tab`, `Alt-A`, `Enter` with its signal, and `Alt-H`,
  and `Alt-H` shows every key in place of the list. The full key list in one
  line was cut on narrow terminals before the keys that matter. While the help
  is shown, any key other than `Ctrl-C` and `Ctrl-Z` only closes it, so
  `Enter` cannot signal a row the user cannot see, and `Esc` returns to the
  list rather than leaving kil.
- `Enter` signals only selected rows drawn on screen. If other selections are
  off screen (scrolled away, pushed off by pinning, or hidden by a resize), it
  signals the rows on screen and keeps kil open with the rest brought on
  screen, so the user sees them before pressing `Enter` again. Signalling them
  unseen, or dropping them silently, were both rejected. `Esc` after such a
  partial `Enter` asks `[Y/n]` before leaving the rest; `Ctrl-C` aborts
  without asking. Keys typed ahead reach kil before the user has seen the
  frames drawn between them, so an `Enter` typed right away could signal rows
  the user has not seen. `Enter` is therefore ignored for 300 ms after the
  help closes, and while a selected row on screen came on screen less than 300
  ms ago, however it came (a partial `Enter`, scrolling, a resize, a query
  edit, or starting kil) and whenever it was selected. Each row's wait is its
  own, so rows that keep coming on screen do not hold `Enter` for rows shown
  long before. Waiting only after a partial `Enter`, or only for rows already
  selected, still let quick keys signal unseen rows, such as `Up`, `Alt-A`,
  `Enter`. An `Enter` ignored so, or during a `Ctrl-R` reload, is not kept for
  later, but the info line says it was ignored, first so that a narrow
  terminal shows it, until nothing holds `Enter`: with nothing on screen, even
  a script did not notice its `Enter` had done nothing. The help and the
  `[Y/n]` question do not say it, as `Enter` closes or answers them. A
  terminal of two lines has no info line, as it keeps a row and the prompt, so
  it does not say it either. Each wait starts once the frame showing its rows
  is on the terminal: started before, a slow frame used it up. A terminal too
  narrow for a row's PID shows no row, so that `Enter` and `Alt-A` cannot take
  a row whose text is cut off entirely. The cursor row alone does not wait, so
  typing a query and pressing `Enter` at once works as in fzf. The picker ends
  as soon as the outcome is decided, and ignores anything handled after it, as
  `Ctrl-C` then `Enter` once signalled rows. `Alt-A` likewise selects only the
  rows on screen and then scrolls to keep the rows it selected there, with the
  rows on screen that were selected before it, so that `Alt-A` again deselects
  them rather than select rows the scroll brought on screen.
- Once the process list is read, kil catches `SIGHUP`, `SIGINT`, `SIGTERM`,
  and `SIGQUIT` and ends with 128 plus the signal's number after restoring the
  terminal and reporting what it signalled. Left to their default actions,
  they ended kil with status 2 or with the terminal in raw mode. Before the
  list is read, nothing is signalled and the terminal is untouched, so the
  default actions stay and a hung `ps` cannot keep kil alive. While kil is
  signalling, including a partial `Enter`, `SIGINT`, `SIGTERM`, and `SIGQUIT`
  stop it as they would `kill(1)`, even during the check before a signal, but
  `SIGHUP` does not: killing the shell kil was started from hangs up the
  terminal, and kil then died before signalling the rest of the selection. A
  second `SIGINT`, `SIGTERM`, or `SIGQUIT` takes its default action, so kil
  ends even if a `ps` hangs. A signal caught while picking also keeps a
  partial `Enter` that arrives after it from starting, and gives kil its
  status even if the terminal fails as it hangs up. Signals are recorded as
  they are caught, with `SIGHUP` on its own channel, as `os/signal` drops what
  does not fit in a channel.
- `Ctrl-Z` and `SIGTSTP` suspend kil after restoring the terminal. The event
  loop suspends kil itself, and tells the picker that kil resumed before it
  handles any key typed meanwhile, so an `Enter` typed at `fg` waits for the
  screen to be drawn again. kil does not stop if a signal was caught while it
  gave the terminal back, and ends if it cannot take the terminal again. kil
  catches `SIGTSTP` to restore the terminal first, and the Go runtime keeps
  swallowing `SIGTSTP` even after `signal.Stop`, so kil stops its process
  group with `SIGSTOP`; a shell may report the job as stopped by a signal.
  Without a shell to bring it back, as when a terminal emulator or tmux runs
  kil directly, its process group is orphaned and `Ctrl-Z` does nothing, since
  kil would stay stopped for good. kil takes its parent being in the same
  session but another process group as the sign of such a shell.
- The check before signalling compares the ps line without the PPID. Killing
  a parent reparents its children, so with the PPID a child selected after
  its parent was skipped as changed and survived.
- A `ps` that fails while kil checks a process before signalling it is
  reported as `failed:` with its reason, not as `skipped:`. ps exits with
  status 1 both for a PID that is gone and for an error, and only an error
  writes to stderr; reported as skipped, a check kil could not make looked
  like a process that had exited. Every `failed:` report gives the reason
  before the line, as `skipped:` does, so the line is always last. ps on macOS
  reports some failures on stderr and still exits 0, so anything on stderr is
  a failure too. What ps wrote goes into the report as one line with control
  characters shown as `?`, as it could otherwise forge report lines or send
  escape sequences to the terminal. kil gives ps `LINES=24` and `COLUMNS=80`
  in place of its own, as procps warns on stderr about a screen size it finds
  bogus, such as a one-row terminal or `LINES=1`, which it reads from the
  terminal unless those are set; kil asks for whole lines anyway.
- On Linux, kil signals through a pidfd and falls back to checking and then
  `kill(2)` only when pidfd also fails for kil itself: opening a pidfd for its
  own PID, or signalling itself with signal 0 through it, as Go's os package
  probes. The system then lacks pidfd or denies it, as seccomp does in some
  containers, and kil would otherwise signal nothing, or report a lack of
  permission the user does not have. A pidfd failure for one target alone, or
  for lack of file descriptors or memory, is reported instead, as Rust's and
  Python's standard libraries also tell those apart, so that kil keeps the
  pidfd's protection against a reused PID wherever it has it.
- Control characters, invalid UTF-8, bidirectional controls, and the line and
  paragraph separators in a ps line become `?`, both when the line is read and
  when a row is drawn. procps does this on Linux except for the last two
  kinds, but ps on macOS and FreeBSD can print a command as it is, so a
  process could repaint the terminal, as CVE-2008-5076 in htop and
  CVE-2026-90773 in procs did, or make its command line read as another one.
  tcell writes control characters to the terminal as they are, and kil's
  drawing reads its colors from escape sequences in the rendered text, so rows
  are cleaned again where they are drawn. The identity the check before
  signalling compares keeps the characters shown as `?`: built from the
  cleaned line, it took a command line changed from one bidirectional control
  to another for the same process.
- Characters of ambiguous East Asian width, such as `·`, `─`, `│`, `▌`, and
  `┃`, count as one column unless `RUNEWIDTH_EASTASIAN=1`, as fzf and
  go-runewidth read it, says the terminal draws them as two. Counted as one on
  such a terminal, every row ran past the right edge. kil does not guess from
  the locale, as many terminals in East Asian locales draw them as one.
  internal/width measures with uniseg, as tcell draws, and keeps uniseg's
  setting, as go-runewidth counted some characters, such as `®`, apart from
  tcell and rows went out of place; internal/ui resets it as it starts, after
  tcell, which reads the variable more loosely. Decorations are sized by their
  real width, the marker of a row not selected as wide as `┃`, a caret too
  wide for the prompt is drawn as a space, and every line is cut to the
  terminal's width last, as on a very narrow terminal indents and ellipses
  alone ran past the edge.
- The check before signalling runs ps once for all the selected processes
  where pidfd works, rather than once per process, which took seconds for a
  few hundred. kil opens a pidfd for every target first, so that a PID reused
  after the check is not what the pidfd points to, in batches as large as the
  open-file limit allows with 64 to spare, up to 256: a batch that took every
  file left ps unable to start and failed every target. A pidfd keeps the
  process but not its command across exec, so kil reads each process's command
  line and name from /proc before the check and again right before its signal,
  and checks a process whose /proc changed, or cannot be read, again on its
  own: ps still decides, as a process can rewrite its command line or name
  itself. Without pidfd, as on macOS, each process is still checked right
  before its signal, as checking all of them first would leave the PID of the
  last one longer to be reused. Once a stop is caught, kil starts no further
  check, as ps can take a while.
