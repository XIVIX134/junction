package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

var procDir = "/proc"

// findProcs returns the PIDs whose command line matches.
func findProcs(match func(args []string) bool) []int {
	ents, err := os.ReadDir(procDir)
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(procDir, e.Name(), "cmdline"))
		if err != nil || len(b) == 0 {
			continue
		}
		args := strings.Split(string(bytes.TrimRight(b, "\x00")), "\x00")
		if match(args) {
			out = append(out, pid)
		}
	}
	return out
}

// serviceDo runs "restart"/"start" on a service with OpenRC or systemd.
func serviceDo(name, action string) error {
	var cmd *exec.Cmd
	if p, err := exec.LookPath("rc-service"); err == nil {
		cmd = exec.Command(p, name, action)
	} else if p, err := exec.LookPath("systemctl"); err == nil {
		cmd = exec.Command(p, action, name)
	} else {
		return fmt.Errorf("neither rc-service nor systemctl is here to %s %s", action, name)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s %s: %v %s", action, name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
