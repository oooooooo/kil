// Command kil filters processes interactively and signals the selected ones.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/oooooooo/kil/internal/picker"
	"github.com/oooooooo/kil/internal/proc"
	"github.com/oooooooo/kil/internal/signal"
	"github.com/oooooooo/kil/internal/ui"
)

const usage = `Usage: kil [--] [query...]

Filter processes interactively and send a signal to the selected ones.
All arguments are joined with spaces into the initial query.

Options:
  -h, --help   Show this help and exit
  --           Treat the remaining arguments as the query (e.g. kil -- -zsh)

Environment:
  KIL_SIGNAL   Signal number or name to send (default: 9, i.e. SIGKILL)

Exit status:
  0    every selected process was signalled
  1    nothing was selected, or some processes were skipped or failed
  2    usage error, invalid KIL_SIGNAL, listing processes failed, or no terminal
  130  aborted with Ctrl-C / Esc
`

const (
	exitOK       = 0
	exitProblems = 1
	exitError    = 2
	exitAborted  = 130
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

type options struct {
	help  bool
	query string
}

func parseArgs(args []string) (options, error) {
	for i, a := range args {
		switch {
		case a == "-h" || a == "--help":
			return options{help: true}, nil
		case a == "--":
			return options{query: strings.Join(args[i+1:], " ")}, nil
		case strings.HasPrefix(a, "-") && a != "-":
			return options{}, fmt.Errorf("unknown option: %s (use 'kil -- %s' to search for it)", a, a)
		default:
			return options{query: strings.Join(args[i:], " ")}, nil
		}
	}
	return options{}, nil
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if err != nil {
		reportf(stderr, "kil: %v\n%s", err, usage)
		return exitError
	}
	if opts.help {
		report(stdout, usage)
		return exitOK
	}
	sig, err := signal.Parse(getenv("KIL_SIGNAL"))
	if err != nil {
		reportf(stderr, "kil: invalid KIL_SIGNAL: %v (use a number like 15 or a name like TERM)\n", err)
		return exitError
	}

	list := func() ([]picker.Item, error) {
		procs, err := proc.List(os.Getpid())
		if err != nil {
			return nil, err
		}
		items := make([]picker.Item, len(procs))
		for i, p := range procs {
			items[i] = picker.Item{ID: p.Line, Text: p.Raw}
		}
		return items, nil
	}
	items, err := list()
	if err != nil {
		reportf(stderr, "kil: failed to list processes: %v; nothing was signalled\n", err)
		return exitError
	}

	model, err := pick(ui.New(items, opts.query, signal.Label(sig), list))
	if err != nil {
		reportf(stderr, "kil: %v\n", err)
		return exitError
	}
	switch model.Outcome() {
	case ui.Aborted:
		reportln(stderr, "kil: aborted")
		return exitAborted
	case ui.NothingSelected:
		reportln(stderr, "kil: nothing selected")
		return exitProblems
	case ui.ListFailed:
		reportf(stderr, "kil: failed to list processes: %v; nothing was signalled\n", model.Err())
		return exitError
	case ui.Accepted:
	default:
		reportln(stderr, "kil: picker ended unexpectedly; nothing was signalled")
		return exitError
	}

	return signalAll(model.Targets(), sig, stdout, stderr)
}

// pick runs the picker on the controlling terminal, so that stdout and
// stderr carry only the results and can be redirected, as with fzf.
func pick(m ui.Model) (ui.Model, error) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return m, fmt.Errorf("no terminal: %w", err)
	}
	defer func() { _ = tty.Close() }()
	final, err := tea.NewProgram(m, tea.WithInput(tty), tea.WithOutput(tty), tea.WithAltScreen()).Run()
	if err != nil {
		return m, fmt.Errorf("picker failed: %w", err)
	}
	fm, ok := final.(ui.Model)
	if !ok {
		return m, errors.New("picker returned an unexpected model")
	}
	return fm, nil
}

func signalAll(targets []picker.Item, sig syscall.Signal, stdout, stderr io.Writer) int {
	code := exitOK
	for _, t := range targets {
		var pid int
		if _, err := fmt.Sscan(t.ID, &pid); err != nil {
			reportf(stderr, "skipped: not a PID: %s\n", t.Text)
			code = exitProblems
			continue
		}
		switch signal.Send(pid, sig, t.ID, proc.Current) {
		case signal.Sent:
			reportf(stdout, "sent %s: %s\n", signal.Label(sig), t.Text)
		case signal.Skipped:
			reportf(stderr, "skipped: process changed or exited: %s\n", t.Text)
			code = exitProblems
		default:
			reportf(stderr, "failed: %s\n", t.Text)
			code = exitProblems
		}
	}
	return code
}

// The report helpers ignore write errors: the exit status already tells the
// caller whether every process was signalled, even if stdout or stderr is
// closed.

func reportf(w io.Writer, format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }

func reportln(w io.Writer, s string) { _, _ = fmt.Fprintln(w, s) }

func report(w io.Writer, s string) { _, _ = fmt.Fprint(w, s) }
