package proc

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"syscall"
)

// States returns the states of the processes with the given PIDs, read from
// /proc/<pid>/stat, with the start time in clock ticks since boot. procps
// prints the start time as the clock less the uptime, which can differ by a
// second between runs, and the process then looked like another one. A
// process that is gone is missing from the map.
func States(pids []int) (map[int]State, error) {
	states := map[int]State{}
	for _, pid := range pids {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		// A process that has just exited gives ESRCH rather than ENOENT.
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
			continue
		}
		if err != nil {
			return nil, err
		}
		// The command name in parentheses may contain spaces and ")".
		f := bytes.Fields(b[bytes.LastIndexByte(b, ')')+1:])
		// The state is the stat file's third field, the start time its
		// 22nd.
		if len(f) < 20 {
			return nil, fmt.Errorf("unexpected /proc/%d/stat: %q", pid, printable(string(b)))
		}
		states[pid] = State{Zombie: string(f[0]) == "Z", Start: string(f[19])}
	}
	return states, nil
}
