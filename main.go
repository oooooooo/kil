// Command kil filters processes interactively and signals the selected ones.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	ossignal "os/signal"
	"strconv"
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
  -e, --exact  Match plain terms exactly, as fzf --exact ('term matches fuzzily)
  -h, --help   Show this help and exit
  --           Treat the remaining arguments as the query (e.g. kil -- -zsh)

Environment:
  KIL_SIGNAL   Signal number or name to send (default: 9, i.e. SIGKILL)
  KIL_EXACT    Set to 1 to start in exact mode, as with -e

Exit status:
  0    every selected process was signalled
  1    nothing was selected, or some processes were skipped or failed
  2    usage error, invalid KIL_SIGNAL or KIL_EXACT, listing processes failed,
       or no terminal
  130  aborted with Ctrl-C / Esc
`

const (
	exitOK       = 0
	exitProblems = 1
	exitError    = 2
	exitAborted  = 130
)

func main() {
	ignoreBrokenPipe()
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// ignoreBrokenPipe makes a write to a closed stdout, as in `kil | head -n 0`,
// fail instead of ending kil with SIGPIPE before every selected process is
// signalled. The report helpers ignore that failure.
func ignoreBrokenPipe() {
	ossignal.Ignore(syscall.SIGPIPE)
}

type options struct {
	help  bool
	exact bool
	query string
}

func parseArgs(args []string) (options, error) {
	var opts options
	for i, a := range args {
		switch {
		case a == "-h" || a == "--help":
			return options{help: true}, nil
		case a == "-e" || a == "--exact":
			opts.exact = true
		case a == "--":
			opts.query = strings.Join(args[i+1:], " ")
			return opts, nil
		case strings.HasPrefix(a, "-") && a != "-":
			return options{}, fmt.Errorf("unknown option: %s (use 'kil -- %s' to search for it)", a, a)
		default:
			opts.query = strings.Join(args[i:], " ")
			return opts, nil
		}
	}
	return opts, nil
}

// exactFromEnv parses KIL_EXACT. An unset or empty value means fuzzy.
func exactFromEnv(v string) (bool, error) {
	if v == "" {
		return false, nil
	}
	exact, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid KIL_EXACT: %q (use 1 or 0)", v)
	}
	return exact, nil
}

func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	opts, err := parseArgs(args)
	if err != nil {
		reportf(stderr, "kil: %v\n%s", err, usage)
		return exitError
	}
	if opts.help {
		// Unlike the reports of signals sent, the usage is the whole
		// output, so failing to write it is an error, as in GNU tools.
		if _, err := fmt.Fprint(stdout, usage); err != nil {
			reportf(stderr, "kil: write error: %v\n", err)
			return exitError
		}
		return exitOK
	}
	envExact, err := exactFromEnv(getenv("KIL_EXACT"))
	if err != nil {
		reportf(stderr, "kil: %v\n", err)
		return exitError
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
			items[i] = picker.Item{ID: p.Identity, Text: p.Raw}
		}
		return items, nil
	}
	items, err := list()
	if err != nil {
		reportf(stderr, "kil: failed to list processes: %v; nothing was signalled\n", err)
		return exitError
	}

	sent := &batches{}
	send := func(batch []picker.Item) {
		sent.code = max(sent.code, signalAll(batch, sig, sent.stdout(), sent.stderr()))
		sent.sent += len(batch)
	}
	model, err := pick(ui.New(items, opts.query, signal.Label(sig), list).WithExact(opts.exact || envExact).WithSender(send))
	if err != nil {
		return pickFailed(sent, err, stdout, stderr)
	}
	return finish(model, sig, sent, stdout, stderr)
}

// pickFailed reports a picker failure after the reports of the rows already
// signalled, which would otherwise be lost.
func pickFailed(sent *batches, err error, stdout, stderr io.Writer) int {
	sent.flush(stdout, stderr)
	reportf(stderr, "kil: %v\n", err)
	return exitError
}

// batches holds the reports of rows signalled before the picker ended,
// which cannot be printed while the picker owns the screen.
type batches struct {
	// reports keeps stdout and stderr reports in one sequence, so that they
	// come out in the order the processes were handled when both go to the
	// same file.
	reports []heldReport
	code    int
	// sent counts the rows handed to signalAll, to tell whether anything
	// may have been signalled.
	sent int
}

type heldReport struct {
	toStderr bool
	text     []byte
}

// heldWriter adds what is written to it to b's reports.
type heldWriter struct {
	b        *batches
	toStderr bool
}

func (w heldWriter) Write(p []byte) (int, error) {
	w.b.reports = append(w.b.reports, heldReport{w.toStderr, bytes.Clone(p)})
	return len(p), nil
}

func (b *batches) stdout() io.Writer { return heldWriter{b, false} }
func (b *batches) stderr() io.Writer { return heldWriter{b, true} }

// flush prints the held reports in order.
func (b *batches) flush(stdout, stderr io.Writer) {
	for _, r := range b.reports {
		w := stdout
		if r.toStderr {
			w = stderr
		}
		_, _ = w.Write(r.text)
	}
	b.reports = nil
}

// signalled says what a failure leaves signalled: after a partial Enter,
// "nothing" would hide the processes reported above it.
func (b *batches) signalled() string {
	if b.sent > 0 {
		return "only the processes reported above were signalled"
	}
	return "nothing was signalled"
}

// finish reports how the picker ended and signals what it accepted.
func finish(model ui.Model, sig syscall.Signal, sent *batches, stdout, stderr io.Writer) int {
	sent.flush(stdout, stderr)
	switch model.Outcome() {
	case ui.Aborted:
		// The user chose to leave, so there is nothing more to report.
		return exitAborted
	case ui.Stopped:
		for _, it := range model.Left() {
			reportf(stderr, "skipped: not signalled: %s\n", it.Text)
		}
		return exitProblems
	case ui.NothingSelected:
		reportln(stderr, "kil: nothing selected")
		return exitProblems
	case ui.ListFailed:
		for _, it := range model.Left() {
			reportf(stderr, "skipped: not signalled: %s\n", it.Text)
		}
		reportf(stderr, "kil: failed to list processes: %v; %s\n", model.Err(), sent.signalled())
		return exitError
	case ui.Accepted:
	default:
		reportf(stderr, "kil: picker ended unexpectedly; %s\n", sent.signalled())
		return exitError
	}

	return max(sent.code, signalAll(model.Targets(), sig, stdout, stderr))
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
