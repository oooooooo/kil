//go:build !linux

package port

import "os"

// Find asks lsof, which macOS has from the start, as gopsutil does: macOS
// gives no other way to map sockets to processes without cgo or the private
// layout of libproc's structures. lsof shows only kil's user's processes
// unless kil runs as root, and gives no trace of the others.
func Find(ports []int) (Result, error) {
	holders, err := lsofHolders(ports)
	if err != nil {
		return Result{}, err
	}
	res := Result{PIDs: map[int]bool{}, MayMiss: os.Geteuid() != 0}
	for _, h := range holders {
		res.PIDs[h.pid] = true
	}
	return res, nil
}
