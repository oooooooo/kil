package port

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
)

// tcpListen is the state /proc/net gives a listening TCP socket. A UDP
// socket holds its port in any state, connected or not.
const tcpListen = "0A"

type socket struct {
	inode uint64
	uid   int
	port  int
}

// Find reads the sockets of kil's network namespace from /proc/net and
// finds the processes holding them through /proc/*/fd, as fuser and procs
// do: unlike ss and lsof, /proc is there in minimal containers.
func Find(ports []int) (Result, error) {
	want := map[int]bool{}
	for _, p := range ports {
		want[p] = true
	}
	all, err := read(want)
	if err != nil {
		return Result{}, err
	}
	res := Result{PIDs: map[int]bool{}, Hidden: map[int][]int{}}
	if len(all) == 0 {
		return res, nil
	}
	byInode := map[uint64]bool{}
	for _, s := range all {
		byInode[s.inode] = false
	}
	if err := holders(byInode, res.PIDs); err != nil {
		return Result{}, err
	}
	// A socket closed after the tables were read has no holder left to
	// find, so the tables are read again before its owner is reported as
	// hidden.
	var unfound []socket
	for _, s := range all {
		if !byInode[s.inode] {
			unfound = append(unfound, s)
		}
	}
	if len(unfound) == 0 {
		return res, nil
	}
	again, err := read(want)
	if err != nil {
		return Result{}, err
	}
	still := map[uint64]bool{}
	for _, s := range again {
		still[s.inode] = true
	}
	for _, s := range unfound {
		if still[s.inode] && !slices.Contains(res.Hidden[s.port], s.uid) {
			res.Hidden[s.port] = append(res.Hidden[s.port], s.uid)
		}
	}
	return res, nil
}

// read returns the sockets on the ports in kil's network namespace.
func read(want map[int]bool) ([]socket, error) {
	var all []socket
	for _, table := range []struct {
		name string
		tcp  bool
	}{{"tcp", true}, {"tcp6", true}, {"udp", false}, {"udp6", false}} {
		b, err := os.ReadFile("/proc/net/" + table.name)
		if errors.Is(err, fs.ErrNotExist) {
			// The kernel has no IPv6.
			continue
		}
		if err != nil {
			return nil, err
		}
		found, err := sockets(string(b), table.tcp, want)
		if err != nil {
			return nil, fmt.Errorf("/proc/net/%s: %w", table.name, err)
		}
		all = append(all, found...)
	}
	return all, nil
}

// sockets returns the sockets in a /proc/net table on the ports: listening
// ones for TCP, any for UDP. A socket without an inode is held by no
// process, as one closing.
func sockets(table string, tcp bool, ports map[int]bool) ([]socket, error) {
	var found []socket
	for i, line := range strings.Split(strings.TrimSpace(table), "\n") {
		if i == 0 {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 10 {
			return nil, fmt.Errorf("unexpected line %q", line)
		}
		_, hexPort, ok := strings.Cut(f[1], ":")
		port, err := strconv.ParseUint(hexPort, 16, 16)
		if !ok || err != nil {
			return nil, fmt.Errorf("unexpected local address %q", f[1])
		}
		uid, err := strconv.Atoi(f[7])
		if err != nil {
			return nil, fmt.Errorf("unexpected uid %q", f[7])
		}
		inode, err := strconv.ParseUint(f[9], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("unexpected inode %q", f[9])
		}
		if (!tcp || f[3] == tcpListen) && ports[int(port)] && inode != 0 {
			found = append(found, socket{inode: inode, uid: uid, port: int(port)})
		}
	}
	return found, nil
}

// holders adds to pids the processes with a file descriptor for a socket
// in inodes, and marks those inodes found. A process whose descriptors kil
// may not read, as another user's, is passed over.
func holders(inodes map[uint64]bool, pids map[int]bool) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		dir := "/proc/" + e.Name() + "/fd/"
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(dir + fd.Name())
			if err != nil {
				continue
			}
			n, ok := strings.CutPrefix(link, "socket:[")
			if !ok {
				continue
			}
			inode, err := strconv.ParseUint(strings.TrimSuffix(n, "]"), 10, 64)
			if err != nil {
				continue
			}
			if _, ok := inodes[inode]; ok {
				inodes[inode] = true
				pids[pid] = true
			}
		}
	}
	return nil
}
