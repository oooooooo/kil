# kil

Currently, to `kill -9` a process, I run `ps | grep` and then `kill -9` by hand.

I want a `kil` command that kills processes by PID or by matching string.

Ideally:

```sh
kil <string>
```

filters the process list in real time, and pressing Enter kills every process shown on screen.

Using fzf is fine, and so is writing it from scratch in Go.
