//go:build linux

package main

import (
	"os/exec"
	"syscall"
)

// diskSpace returns the free (for non-root) and total bytes of the filesystem holding path.
func diskSpace(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}

// detach starts cmd in its own session, so stopping Junction doesn't stop it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

func sighup(pid int) error { return syscall.Kill(pid, syscall.SIGHUP) }
