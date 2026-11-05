package signal

import "golang.org/x/sys/unix"

// kill is kill(2). Tests replace it to make a signal fail.
var kill = unix.Kill
