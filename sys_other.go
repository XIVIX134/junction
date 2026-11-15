//go:build !linux

package main

// Stubs so Junction builds (and its tests run) off Linux; it only runs on Linux.

import (
	"errors"
	"os/exec"
)

func diskSpace(string) (free, total uint64, err error) {
	return 0, 0, errors.New("not available on this system")
}

func detach(*exec.Cmd) {}
