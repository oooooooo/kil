// Package proc lists processes with ps, which unlike /proc works the same on
// Linux and macOS.
package proc

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// psColumns is the column list for ps -o. comm can contain spaces (e.g.
// "tmux: server"), so only the PID and PPID are parsed out of a line; the
// rest of the line is kept as the process's identity and display text.
const psColumns = "pid=,ppid=,user=,comm=,args="

// psWide makes ps print whole lines on Linux and macOS: without it, ps cuts
// them to COLUMNS when that is set, hiding the end of long command lines from
// matching and from the check before signalling.
const psWide = "-ww"

// A Process is one line of ps output.
type Process struct {
	PID  int
	PPID int
	// Identity is the ps line without the PPID and with runs of whitespace
	// collapsed, so that lines from different ps invocations compare equal
	// despite column padding. The PPID is left out because killing a parent
	// reparents its children: a child selected with its parent is still the
	// same process and must not be skipped as changed.
	Identity string
	// Raw is the ps line as printed, with its column padding, for display
	// and matching, as fzf would see it.
	Raw string
}

// Parse parses ps output produced with psColumns.
func Parse(out string) ([]Process, error) {
	var procs []Process
	for raw := range strings.Lines(out) {
		raw = printable(strings.TrimRight(raw, "\n"))
		fields := strings.Fields(raw)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 2 {
			return nil, fmt.Errorf("unexpected ps line: %q", raw)
		}
		pid, err := strconv.Atoi(fields[0])
		// Only positive PIDs: kill(2) treats 0 and negative values as process
		// groups, and -1 as every process the user may signal.
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("unexpected PID in ps line: %q", raw)
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil || ppid < 0 {
			return nil, fmt.Errorf("unexpected PPID in ps line: %q", raw)
		}
		procs = append(procs, Process{
			PID:      pid,
			PPID:     ppid,
			Identity: strings.Join(append([]string{fields[0]}, fields[2:]...), " "),
			Raw:      raw,
		})
	}
	return procs, nil
}

// printable replaces control characters and invalid UTF-8 in a ps line with
// "?", as procps prints them on Linux. ps on macOS and FreeBSD can print a
// command or command line as it is, so a process could otherwise put
// terminal control sequences in it and repaint kil's screen or the terminal
// kil reports to. Bidirectional controls and the line and paragraph
// separators, which procps keeps, become "?" too, as they can reorder or
// break the line on screen so that it reads as another command.
func printable(line string) string {
	var b strings.Builder
	for _, r := range strings.ToValidUTF8(line, "\uFFFD") {
		if r == utf8.RuneError || unsafeOnScreen(r) {
			r = '?'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// unsafeOnScreen reports whether r, written to a terminal, could do more
// than show a character. internal/ui draws rows with the same rule.
func unsafeOnScreen(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029'
}

// HideDescendants returns procs without self and its descendants, so kil
// does not offer to signal itself or the ps it runs.
func HideDescendants(procs []Process, self int) []Process {
	parent := make(map[int]int, len(procs))
	for _, p := range procs {
		parent[p.PID] = p.PPID
	}
	var kept []Process
	for _, p := range procs {
		if !descendsFrom(p.PID, self, parent) {
			kept = append(kept, p)
		}
	}
	return kept
}

func descendsFrom(pid, ancestor int, parent map[int]int) bool {
	// The step cap guards against PPID cycles in a snapshot taken while
	// processes are exiting.
	for steps := 0; steps <= len(parent) && pid != 0; steps++ {
		if pid == ancestor {
			return true
		}
		next, ok := parent[pid]
		if !ok {
			return false
		}
		pid = next
	}
	return false
}

// psSyntax lists the variables procps reads its option syntax from. With
// BSD syntax, such as PS_PERSONALITY=bsd, it rejects kil's options.
var psSyntax = []string{"PS_PERSONALITY", "CMD_ENV", "I_WANT_A_BROKEN_PS"}

// ps returns a ps command printing psColumns of the selected processes.
func ps(selection ...string) *exec.Cmd {
	cmd := exec.Command("ps", append(append([]string{psWide}, selection...), "-o", psColumns)...)
	cmd.Env = slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(psSyntax, name)
	})
	return cmd
}

// List returns every process except self and its descendants. Pass 0 to
// hide nothing. A ps failure is an error rather than a short list, because
// a partial list could mislead the user about what is running.
func List(self int) ([]Process, error) {
	out, err := ps("-A").Output()
	if err != nil {
		return nil, fmt.Errorf("ps failed: %w", err)
	}
	procs, err := Parse(string(out))
	if err != nil {
		return nil, err
	}
	if self == 0 {
		return procs, nil
	}
	return HideDescendants(procs, self), nil
}

// Current returns the ps line of pid now, or false if pid does not exist.
// A ps that fails is an error rather than a missing process, so that kil does
// not report a process it could not check as one that exited.
func Current(pid int) (string, bool, error) {
	cmd := ps("-p", strconv.Itoa(pid))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	// ps, on Linux and macOS, exits with status 1 and prints nothing when no
	// process has the PID. It also exits with 1 on errors, but then says why.
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) == 0 && stderr.Len() == 0 {
		return "", false, nil
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", false, fmt.Errorf("ps failed: %w: %s", err, msg)
		}
		return "", false, fmt.Errorf("ps failed: %w", err)
	}
	procs, err := Parse(string(out))
	if err != nil {
		return "", false, err
	}
	if len(procs) != 1 {
		return "", false, fmt.Errorf("ps listed %d processes for PID %d", len(procs), pid)
	}
	return procs[0].Identity, true, nil
}
