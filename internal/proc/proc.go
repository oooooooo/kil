// Package proc lists processes with ps, which unlike /proc works the same on
// Linux and macOS.
package proc

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// psColumns is the column list for ps -o. comm can contain spaces (e.g.
// "tmux: server"), so only the PID and PPID are parsed out of a line; the
// whole line is kept as the process's identity and display text.
const psColumns = "pid=,ppid=,user=,comm=,args="

// A Process is one line of ps output.
type Process struct {
	PID  int
	PPID int
	// Line is the ps line with runs of whitespace collapsed, so that lines
	// from different ps invocations compare equal despite column padding.
	Line string
}

// Parse parses ps output produced with psColumns.
func Parse(out string) ([]Process, error) {
	var procs []Process
	for raw := range strings.Lines(out) {
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
		procs = append(procs, Process{PID: pid, PPID: ppid, Line: strings.Join(fields, " ")})
	}
	return procs, nil
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

// List returns every process except self and its descendants. Pass 0 to
// hide nothing. A ps failure is an error rather than a short list, because
// a partial list could mislead the user about what is running.
func List(self int) ([]Process, error) {
	out, err := exec.Command("ps", "-A", "-o", psColumns).Output()
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
func Current(pid int) (string, bool) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", psColumns).Output()
	if err != nil {
		return "", false
	}
	procs, err := Parse(string(out))
	if err != nil || len(procs) != 1 {
		return "", false
	}
	return procs[0].Line, true
}
