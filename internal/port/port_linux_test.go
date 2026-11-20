package port

import (
	"slices"
	"testing"
)

const tcp = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 111 1 0000000000000000 100 0 0 10 0
   1: 0100007F:0BB8 0100007F:D431 01 00000000:00000000 00:00000000 00000000  1000        0 112 1 0000000000000000 20 4 30 10 -1
   2: 0100007F:D431 0100007F:0BB8 01 00000000:00000000 00:00000000 00000000  1000        0 113 1 0000000000000000 20 4 30 10 -1
   3: 00000000:1435 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 114 1 0000000000000000 100 0 0 10 0
   4: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 116 1 0000000000000000 100 0 0 10 0
   5: 00000000:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 0 1 0000000000000000 100 0 0 10 0
`

const udp6 = `  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode ref pointer drops
  10: 00000000000000000000000000000000:0BB8 00000000000000000000000000000000:0000 07 00000000:00000000 00:00000000 00000000   501        0 115 2 0000000000000000 0
  12: 0000000000000000FFFF00000100007F:0BB8 0000000000000000FFFF00000100007F:D431 01 00000000:00000000 00:00000000 00000000   501        0 118 2 0000000000000000 0
  11: 00000000000000000000000000000000:1F90 00000000000000000000000000000000:0000 07 00000000:00000000 00:00000000 00000000   501        0 117 2 0000000000000000 0
`

// Only listening TCP sockets and UDP ones bound to the ports, connected or
// not, count, with
// the user that owns them; a connection from or to the port does not, nor
// a socket without an inode, which no process holds.
func TestSockets(t *testing.T) {
	ports := map[int]bool{3000: true, 5173: true}
	got, err := sockets(tcp, true, ports)
	want := []socket{{inode: 111, uid: 1000, port: 3000}, {inode: 114, uid: 0, port: 5173}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("sockets(tcp) = %v, %v; want %v", got, err, want)
	}
	got, err = sockets(udp6, false, ports)
	want = []socket{{inode: 115, uid: 501, port: 3000}, {inode: 118, uid: 501, port: 3000}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("sockets(udp6) = %v, %v; want %v", got, err, want)
	}
	if _, err := sockets("  sl local\n   0: garbage\n", true, ports); err == nil {
		t.Error("sockets(garbage) succeeded, want an error")
	}
}
