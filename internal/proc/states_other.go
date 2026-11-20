//go:build !linux

package proc

import (
	"fmt"
	"strconv"
	"strings"
)

// States returns the states of the processes with the given PIDs, as ps
// gives them, with the start time it prints, which on macOS comes from the
// kernel as it is. A process that is gone is missing from the map.
func States(pids []int) (map[int]State, error) {
	states := map[int]State{}
	if len(pids) == 0 {
		return states, nil
	}
	list := make([]string, len(pids))
	for i, pid := range pids {
		list[i] = strconv.Itoa(pid)
	}
	out, err := output("pid=,stat=,lstart=", "-p", strings.Join(list, ","))
	if err != nil {
		return nil, err
	}
	for line := range strings.Lines(out) {
		f := strings.Fields(line)
		if len(f) < 3 {
			return nil, fmt.Errorf("unexpected ps output: %q", printable(line))
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("unexpected ps output: %q", printable(line))
		}
		states[pid] = State{Zombie: strings.HasPrefix(f[1], "Z"), Start: strings.Join(f[2:], " ")}
	}
	return states, nil
}
