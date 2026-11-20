package port

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// A holder is a process holding a socket, with the socket's local address.
type holder struct {
	pid   int
	local string
}

// lsofHolders runs lsof for the sockets on the ports: listening TCP ones
// and UDP ones whose local port is one of them. lsof matches either end of
// a socket, so a client whose remote end is the port is left out here, by
// the local address in its name. The TCP state is read from lsof's output
// rather than selected with -sTCP:LISTEN, which on Linux's lsof also
// dropped every UDP socket.
// lsofBatch is how many ports one lsof run looks up: lsof on macOS takes at
// most 100 -i selectors, and each port takes two. Tests change it.
var lsofBatch = 50

func lsofHolders(ports []int) ([]holder, error) {
	var all []holder
	for start := 0; start < len(ports); start += lsofBatch {
		found, err := lsofRun(ports[start:min(start+lsofBatch, len(ports))])
		if err != nil {
			return nil, err
		}
		all = append(all, found...)
	}
	return all, nil
}

// lsofRun looks the ports up in one lsof run.
func lsofRun(ports []int) ([]holder, error) {
	want := map[string]bool{}
	args := []string{"-nP", "-FpfPnT"}
	for _, p := range ports {
		want[strconv.Itoa(p)] = true
		args = append(args, "-iTCP:"+strconv.Itoa(p), "-iUDP:"+strconv.Itoa(p))
	}
	cmd := exec.Command("lsof", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		// lsof ends with 1 when any selector matched nothing, as the UDP
		// one for a TCP server does, even with others' matches printed.
		// Only warnings may come with that.
		for line := range strings.Lines(stderr.String()) {
			if !strings.HasPrefix(line, "lsof: WARNING") {
				return nil, fmt.Errorf("lsof failed: %s", strings.TrimSpace(stderr.String()))
			}
		}
	case err != nil:
		return nil, fmt.Errorf("lsof failed: %w", err)
	}
	var holders []holder
	pid := 0
	// A file's fields come in order, from its descriptor, which -F gives
	// only when asked, to its state after its name, so each is taken once
	// the next file or process starts.
	var proto, name string
	listening := false
	take := func() {
		local, _, _ := strings.Cut(name, "->")
		i := strings.LastIndexByte(local, ':')
		if i >= 0 && want[local[i+1:]] && (proto == "UDP" || proto == "TCP" && listening) {
			holders = append(holders, holder{pid: pid, local: local})
		}
		proto, name, listening = "", "", false
	}
	for line := range strings.Lines(string(out)) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			take()
			if pid, err = strconv.Atoi(line[1:]); err != nil {
				return nil, fmt.Errorf("unexpected lsof output %q", line)
			}
		case 'f':
			take()
		case 'P':
			proto = line[1:]
		case 'n':
			name = line[1:]
		case 'T':
			if line[1:] == "ST=LISTEN" {
				listening = true
			}
		}
	}
	take()
	return holders, nil
}
